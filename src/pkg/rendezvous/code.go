package rendezvous

import (
	"context"
	"crypto/sha256"
	"io"
	"strings"
	"time"

	"github.com/tmc/go-iroh/dns"
	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/key"
	"golang.org/x/crypto/hkdf"

	"github.com/bresilla/drop/src/pkg/node"
)

// A short code in place of a ticket.
//
// A ticket is a machine's id and a code, and the id is sixty-four characters nobody types across a
// desk. So a machine showing a code also says, under a key only that code works out, which machine
// it is; whoever types the code looks that up, and has the id without ever seeing it. The relay
// holding the record learns a key and an id and never the code, and the code is only good while
// it is being shown.

// codeKey is the key a code's record is kept under.
func codeKey(code string) key.SecretKey {
	var seed [32]byte
	_, _ = io.ReadFull(hkdf.New(sha256.New, []byte(NormalCode(code)), nil, []byte("drop machine code/1")), seed[:])
	return key.NewSecretKey(seed)
}

// NormalCode is a code as it is compared: what somebody typed, whatever case and spacing they
// typed it in.
func NormalCode(code string) string {
	return strings.ToLower(strings.Join(strings.Fields(code), "-"))
}

// PublishCode says, under the code's key, that this is the machine showing it, for as long as ctx
// lasts.
func PublishCode(ctx context.Context, code string, id node.ID) error {
	publisher, err := iroh.N0PkarrPublisher(codeKey(code), &iroh.PkarrPublisherConfig{})
	if err != nil {
		return err
	}
	said, err := dns.NewUserData(id.String())
	if err != nil {
		_ = publisher.Close()
		return err
	}
	publisher.Publish(dns.NewEndpointData().WithUserData(&said))
	go func() {
		<-ctx.Done()
		_ = publisher.Close()
	}()
	return nil
}

// FindCode is the machine showing a code, looked up for as long as ctx allows: a record a moment
// old may not have reached the relay yet.
func (o *Openly) FindCode(ctx context.Context, code string) (node.ID, bool) {
	at := codeKey(code).Public().EndpointID()
	for delay := o.retryMin; ; delay = min(2*delay, o.retryMax) {
		for item, err := range o.resolver.Resolve(ctx, at) {
			if err != nil {
				continue
			}
			if said := item.EndpointInfo().Data.UserData(); said != nil {
				if id, err := node.ParseID(said.String()); err == nil {
					return id, true
				}
			}
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return node.ID{}, false
		case <-timer.C:
		}
	}
}

package rendezvous

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"strings"
	"time"

	"github.com/tmc/go-iroh/dns"
	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/key"
	"github.com/tmc/go-iroh/netaddr"
	"golang.org/x/crypto/hkdf"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
)

// A doorbell: one person's machines finding each other with nothing but the key.
//
// Two machines that have met find each other under a secret they made when they met. A machine
// that holds its person's key and has met none of their other machines has no such secret, so it
// rings instead: it says where it is under an identity worked out from the public key, with a proof
// that the key signed for it, and the rest of that person's machines look there.
//
// Anybody who knows the public key can look too. So a machine rings only while it knows no other
// machine of its person's, and says only where it is and what its badge already says.

const doorbellInfo = "drop doorbell v1"

// RungFresh is how long a machine that rang is found where it said, without asking again.
const RungFresh = 15 * time.Minute

// doorbellKey is the identity a person's doorbell is kept under in one epoch.
func doorbellKey(user []byte, epoch int64) key.SecretKey {
	var at [8]byte
	binary.BigEndian.PutUint64(at[:], uint64(epoch))
	var seed [32]byte
	_, _ = io.ReadFull(hkdf.New(sha256.New, user, nil, append([]byte(doorbellInfo), at[:]...)), seed[:])
	return key.NewSecretKey(seed)
}

// Rung is a machine ringing: which one, where it is, and what it said to prove whose it is.
type Rung struct {
	ID    node.ID
	Addr  netaddr.EndpointAddr
	Proof string
	at    time.Time
}

// Ring has this machine ring the doorbell of a user key, with a proof, from the next round on and
// every round after. An empty proof stops it ringing.
func (s *Service) Ring(user []byte, proof string) {
	s.mu.Lock()
	same := string(s.bellUser) == string(user) && s.bellProof == proof
	s.bellUser, s.bellProof = append([]byte(nil), user...), proof
	s.mu.Unlock()
	if !same {
		select {
		case s.rang <- struct{}{}:
		default:
		}
	}
}

// Ringing reports whether this machine is ringing.
func (s *Service) Ringing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bellProof != ""
}

// ringRound publishes this machine's ring for this epoch and the next, and says which records it
// kept up.
func (s *Service) ringRound(now time.Time, data dns.EndpointData, live map[string]bool) {
	s.mu.Lock()
	user, proof := s.bellUser, s.bellProof
	s.mu.Unlock()
	if proof == "" {
		return
	}
	said, err := dns.NewUserData(s.node.ID().String() + " " + proof)
	if err != nil {
		return
	}
	ring := data.WithUserData(&said)
	for _, epoch := range PublishEpochs(now) {
		sk := doorbellKey(user, epoch)
		at := sk.Public().EndpointID().String()
		live[at] = true

		s.mu.Lock()
		p, ok := s.publishers[at]
		if !ok {
			p, err = iroh.NewPkarrPublisher(sk, s.relay, &iroh.PkarrPublisherConfig{AddrFilter: node.Filter()})
			if err != nil {
				s.mu.Unlock()
				continue
			}
			s.publishers[at] = p
		}
		s.mu.Unlock()
		p.Publish(ring)
	}
}

// Answer is every other machine ringing a user key's doorbell now. What it hears is kept for a
// while, so dialling one of them finds it where it rang from.
func (s *Service) Answer(ctx context.Context, user []byte) []Rung {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	var out []Rung
	seen := map[node.ID]bool{}
	for _, epoch := range ResolveEpochs(time.Now()) {
		for item, err := range s.resolver.Resolve(ctx, doorbellKey(user, epoch).Public().EndpointID()) {
			if err != nil {
				continue
			}
			info := item.EndpointInfo()
			said := info.Data.UserData()
			if said == nil {
				continue
			}
			who, proof, _ := strings.Cut(said.String(), " ")
			id, err := node.ParseID(who)
			if err != nil || id == s.node.ID() || seen[id] {
				break
			}
			if addr, ok := rebind(info, id); ok {
				seen[id] = true
				out = append(out, Rung{ID: id, Addr: addr, Proof: proof, at: time.Now()})
			}
			break
		}
	}

	s.mu.Lock()
	for _, r := range out {
		s.rung[r.ID] = r
	}
	s.mu.Unlock()
	return out
}

// rungAt is where a machine that rang a moment ago said it was.
func (s *Service) rungAt(entry book.Entry) (netaddr.EndpointAddr, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rung[entry.ID]
	if !ok || time.Since(r.at) > RungFresh {
		delete(s.rung, entry.ID)
		return netaddr.EndpointAddr{}, false
	}
	return r.Addr, true
}

//go:build android

package gui

import (
	"errors"
	"image"
	"sync"
	"time"

	_ "gioui.org/app/permission/camera"
)

// Reading a code on a phone: Android's camera, and the same Go decoder the browser uses.
//
// The preview is drawn by the interface rather than by a system view, so what is on screen is one
// surface and not a hole punched through it. That costs a copy of the brightness plane per frame,
// which at a few frames a second is nothing next to what the decoding costs.

var phone struct {
	mu      sync.Mutex
	running bool
	stop    chan struct{}
	frame   *image.Gray
}

// canScan is true on any phone: whether there is a camera to open is answered by opening it, and a
// device with none is rare enough not to hide the button for.
func canScan() bool { return true }

func startScan(to scanning) error {
	phone.mu.Lock()
	if phone.running {
		phone.mu.Unlock()
		return errors.New("already scanning")
	}
	phone.running = true
	phone.stop = make(chan struct{})
	stop := phone.stop
	phone.mu.Unlock()

	go func() {
		defer func() {
			closeCamera()

			phone.mu.Lock()
			phone.running, phone.frame = false, nil
			phone.mu.Unlock()
		}()

		if !allowed(30 * time.Second) {
			to.failed(errors.New("drop was not allowed to use the camera"))
			return
		}
		if err := openCamera(); err != nil {
			to.failed(err)
			return
		}

		// One buffer, reused: a frame is half a megabyte and there is no reason for the collector
		// to see a new one several times a second.
		into := make([]byte, frameWidth*frameHeight*2)

		for {
			select {
			case <-stop:
				return
			case <-time.After(120 * time.Millisecond):
			}

			frame := nextFrame(into)
			if frame == nil {
				continue
			}

			phone.mu.Lock()
			phone.frame = frame
			phone.mu.Unlock()

			text, err := readCode(frame)
			if err != nil || text == "" {
				continue
			}
			to.found(text)
			return
		}
	}()

	return nil
}

func stopScan() {
	phone.mu.Lock()
	defer phone.mu.Unlock()

	if phone.stop != nil {
		close(phone.stop)
		phone.stop = nil
	}
}

// scanFrame is what the camera is looking at, for the interface to draw. Nil when nothing is.
func scanFrame() image.Image {
	phone.mu.Lock()
	defer phone.mu.Unlock()

	if !phone.running || phone.frame == nil {
		return nil
	}
	return phone.frame
}

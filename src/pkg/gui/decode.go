//go:build js || android

package gui

import (
	"errors"
	"image"

	"github.com/liyue201/goqr"
)

// readCode finds a QR code in one frame from a camera.
//
// The decoding is Go rather than the platform's, because the platforms disagree about whether they
// have a decoder at all: a browser on a phone usually does, the same browser on a desktop usually
// does not, and a code that only works on some of them is worse than one that is a little slower
// everywhere.
func readCode(frame image.Image) (string, error) {
	found, err := goqr.Recognize(frame)
	if err != nil {
		return "", err
	}
	for _, code := range found {
		if text := string(code.Payload); text != "" {
			return text, nil
		}
	}
	return "", errors.New("no code in this frame")
}

package events

import (
	"errors"

	"github.com/s1liconcow/skiff/internal/state/canonical"
)

func Link(previous *Event, current *Event) error {
	if current == nil {
		return errors.New("current event is required")
	}
	var previousID, previousHash string
	if previous != nil {
		previousID = previous.ID
		if previous.HashChain != nil {
			previousHash = previous.HashChain.Hash
		}
		if previousHash == "" {
			hash, err := Digest(*previous)
			if err != nil {
				return err
			}
			previousHash = hash
		}
	}
	hash, err := digestWithPrevious(*current, previousHash)
	if err != nil {
		return err
	}
	current.HashChain = &HashChain{
		PreviousEventID: previousID,
		PreviousHash:    previousHash,
		Hash:            hash,
	}
	return nil
}

func Digest(event Event) (string, error) {
	return digestWithPrevious(event, "")
}

func digestWithPrevious(event Event, previousHash string) (string, error) {
	event.HashChain = nil
	body, err := canonical.Marshal(struct {
		PreviousHash string `json:"previous_hash,omitempty"`
		Event        Event  `json:"event"`
	}{
		PreviousHash: previousHash,
		Event:        event,
	})
	if err != nil {
		return "", err
	}
	return hexSHA256(body), nil
}

func VerifyChain(events []Event) error {
	var previous *Event
	for i := range events {
		event := &events[i]
		if event.HashChain == nil {
			previous = event
			continue
		}
		expectedPreviousID := ""
		expectedPreviousHash := ""
		if previous != nil {
			expectedPreviousID = previous.ID
			if previous.HashChain != nil {
				expectedPreviousHash = previous.HashChain.Hash
			}
			if expectedPreviousHash == "" {
				hash, err := Digest(*previous)
				if err != nil {
					return err
				}
				expectedPreviousHash = hash
			}
		}
		if event.HashChain.PreviousEventID != expectedPreviousID {
			return errors.New("event hash chain previous event id mismatch")
		}
		if event.HashChain.PreviousHash != expectedPreviousHash {
			return errors.New("event hash chain previous hash mismatch")
		}
		hash, err := digestWithPrevious(*event, expectedPreviousHash)
		if err != nil {
			return err
		}
		if event.HashChain.Hash != hash {
			return errors.New("event hash chain digest mismatch")
		}
		previous = event
	}
	return nil
}

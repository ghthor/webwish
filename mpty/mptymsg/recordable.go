package mptymsg

import (
	"encoding/gob"
	"encoding/json"
	"fmt"
	"time"
)

type Recordable interface {
	// TypeName is used during encoding/decoding to determine what the payload
	// of each messages concrete type is. You should NOT use the golang %T as
	// that will change if you move the type to a different package.
	TypeName() string

	Ts() time.Time

	SetId(int64) Recordable
}

var decoders = make(map[string]func(data []byte) (Recordable, error))

func Register[T Recordable](t T) {
	gob.Register(t)

	decoders[t.TypeName()] = func(data []byte) (Recordable, error) {
		var v T
		err := json.Unmarshal(data, &v)
		if err != nil {
			return nil, err
		}
		return v, nil
	}
}

type Envelope struct {
	Type    string
	Payload json.RawMessage
}

type EnvelopeEncode struct {
	Type    string
	Payload any
}

// JsonMarshal returns the Recordable message as json bytes. To decode the
// value you must Register(t) first.
func JsonMarshal[T Recordable](t T) ([]byte, error) {
	return json.Marshal(EnvelopeEncode{
		Type:    t.TypeName(),
		Payload: t,
	})
}

// JsonUnmarshal will decode a Recordable message from json bytes. You must
// Register(t) any types at least once before attempting to decode them.
func JsonUnmarshal(data []byte) (Recordable, error) {
	var e Envelope
	err := json.Unmarshal(data, &e)
	if err != nil {
		return nil, err
	}

	d := decoders[e.Type]
	if d == nil {
		return nil, fmt.Errorf("unregistered mptymsg type: %s", e.Type)
	}

	return d(e.Payload)
}

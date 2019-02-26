package gun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

// DefaultSoulGen is the default soul generator. It uses the current time in
// MS as the first part of the string. However if that MS was already used in
// this process, the a guaranteed-process-unique-nano-level time is added to
// the first part. The second part is a random string.
func DefaultSoulGen() string {
	ms, uniqueNum := timeNowUniqueUnix()
	s := strconv.FormatInt(ms, 36)
	if uniqueNum > 0 {
		s += strconv.FormatInt(uniqueNum, 36)
	}
	return s + randString(12)
}

// Node is a JSON-encodable representation of a Gun node which is a set of
// scalar values by name and metadata about those values.
type Node struct {
	// Metadata is the metadata for this node.
	Metadata
	// Values is the set of values (including null) keyed by the field name.
	Values map[string]Value
}

// MarshalJSON implements encoding/json.Marshaler for Node.
func (n *Node) MarshalJSON() ([]byte, error) {
	// Just put it all in a map and then encode it
	toEnc := make(map[string]interface{}, len(n.Values)+1)
	toEnc["_"] = &n.Metadata
	for k, v := range n.Values {
		toEnc[k] = v
	}
	return json.Marshal(toEnc)
}

// UnmarshalJSON implements encoding/json.Unmarshaler for Node.
func (n *Node) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	// We'll just go from start brace to end brace
	if t, err := dec.Token(); err != nil {
		return err
	} else if t != json.Delim('{') {
		return fmt.Errorf("Unexpected token %v", t)
	}
	n.Values = map[string]Value{}
	for {
		if key, err := dec.Token(); err != nil {
			return err
		} else if key == json.Delim('}') {
			return nil
		} else if keyStr, ok := key.(string); !ok {
			return fmt.Errorf("Unrecognized token %v", key)
		} else if keyStr == "_" {
			if err = dec.Decode(&n.Metadata); err != nil {
				return fmt.Errorf("Failed unmarshaling metadata: %v", err)
			}
		} else if val, err := dec.Token(); err != nil {
			return err
		} else if n.Values[keyStr], err = ValueDecodeJSON(val, dec); err != nil {
			return err
		}
	}
}

// Metadata is the soul of a node and the state of its values.
type Metadata struct {
	// Soul is the unique identifier of this node.
	Soul string `json:"#,omitempty"`
	// State is the conflict state value for each node field.
	State map[string]State `json:">,omitempty"`
}

// Value is the common interface implemented by all possible Gun values. The
// possible values of Value are nil and instances of ValueNumber, ValueString,
// ValueBool, and ValueRelation. They can all be marshaled into JSON.
type Value interface {
	nodeValue()
}

// ValueDecodeJSON decodes a single Value from JSON. For the given JSON decoder
// and last read token, this decodes a Value. The decoder needs to have ran
// UseNumber. Unrecognized values are errors.
func ValueDecodeJSON(token json.Token, dec *json.Decoder) (Value, error) {
	switch token := token.(type) {
	case nil:
		return nil, nil
	case json.Number:
		return ValueNumber(token), nil
	case string:
		return ValueString(token), nil
	case bool:
		return ValueBool(token), nil
	case json.Delim:
		if token != json.Delim('{') {
			return nil, fmt.Errorf("Unrecognized token %v", token)
		} else if relKey, err := dec.Token(); err != nil {
			return nil, err
		} else if relKey != "#" {
			return nil, fmt.Errorf("Unrecognized token %v", relKey)
		} else if relVal, err := dec.Token(); err != nil {
			return nil, err
		} else if relValStr, ok := relVal.(string); !ok {
			return nil, fmt.Errorf("Unrecognized token %v", relVal)
		} else if endTok, err := dec.Token(); err != nil {
			return nil, err
		} else if endTok != json.Delim('}') {
			return nil, fmt.Errorf("Unrecognized token %v", endTok)
		} else {
			return ValueRelation(relValStr), nil
		}
	default:
		return nil, fmt.Errorf("Unrecognized token %v", token)
	}
}

// ValueString is a representation of a string Value.
type ValueString string

func (ValueString) nodeValue()       {}
func (v ValueString) String() string { return string(v) }

// ValueNumber is a representation of a number Value. It is typed as a string
// similar to how encoding/json.Number works since it can overflow numeric
// types.
type ValueNumber string

func (ValueNumber) nodeValue()       {}
func (v ValueNumber) String() string { return string(v) }

// Float64 returns the number as a float64.
func (v ValueNumber) Float64() (float64, error) { return strconv.ParseFloat(string(v), 64) }

// Int64 returns the number as an int64.
func (v ValueNumber) Int64() (int64, error) { return strconv.ParseInt(string(v), 10, 64) }

// ValueBool is a representation of a bool Value.
type ValueBool bool

func (ValueBool) nodeValue() {}

// ValueRelation is a representation of a relation Value. The value is the soul
// of the linked node. It has a custom JSON encoding.
type ValueRelation string

func (ValueRelation) nodeValue()       {}
func (v ValueRelation) String() string { return string(v) }

// MarshalJSON implements encoding/json.Marshaler fr ValueRelation.
func (v ValueRelation) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"#": string(v)})
}

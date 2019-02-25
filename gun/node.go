package gun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

func DefaultSoulGen() string {
	ms, uniqueNum := timeNowUniqueUnix()
	s := strconv.FormatInt(ms, 36)
	if uniqueNum > 0 {
		s += strconv.FormatInt(uniqueNum, 36)
	}
	return s + randString(12)
}

type Node struct {
	Metadata
	Values map[string]Value
}

func (n *Node) MarshalJSON() ([]byte, error) {
	// Just put it all in a map and then encode it
	toEnc := make(map[string]interface{}, len(n.Values)+1)
	toEnc["_"] = &n.Metadata
	for k, v := range n.Values {
		toEnc[k] = v
	}
	return json.Marshal(toEnc)
}

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

type Metadata struct {
	Soul  string           `json:"#,omitempty"`
	State map[string]State `json:">,omitempty"`
}

// TODO: put private method to seal enum
type Value interface {
	nodeValue()
}

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

type ValueString string

func (ValueString) nodeValue() {}

type ValueNumber string

func (ValueNumber) nodeValue() {}

type ValueBool bool

func (ValueBool) nodeValue() {}

type ValueRelation string

func (ValueRelation) nodeValue() {}

func (n ValueRelation) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"#": string(n)})
}

// type ValueWithState struct {
// 	Value Value
// 	// This is 0 for top-level values
// 	State int64
// }

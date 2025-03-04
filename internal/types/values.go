package types

import "github.com/ghettovoice/gosip/internal/util"

// Values maps a string key to a list of string values.
// The keys in the map are case-insensitive.
// It is typically used to store URI's or header's parameters.
type Values map[string][]string

// Get returns values associated with the given key.
// If there are no values associated with the key, Get returns the empty slice.
func (vals Values) Get(key string) []string { return vals[util.LCase(key)] }

func (vals Values) First(key string) (string, bool) {
	v := vals[util.LCase(key)]
	if len(v) == 0 {
		return "", false
	}
	return v[0], true
}

func (vals Values) Last(key string) (string, bool) {
	v := vals[util.LCase(key)]
	if len(v) == 0 {
		return "", false
	}
	return v[len(v)-1], true
}

// Set sets the key to value. It replaces any existing values.
func (vals Values) Set(key, value string) Values {
	vals[util.LCase(key)] = []string{value}
	return vals
}

func (vals Values) Append(key, value string) Values {
	key = util.LCase(key)
	vals[key] = append(vals[key], value)
	return vals
}

func (vals Values) Prepend(key, value string) Values {
	key = util.LCase(key)
	vals[key] = append([]string{value}, vals[key]...)
	return vals
}

// Del deletes the values associated with the key.
func (vals Values) Del(key string) Values {
	delete(vals, util.LCase(key))
	return vals
}

// Has checks whether a given key is in the list.
func (vals Values) Has(key string) bool {
	_, ok := vals[util.LCase(key)]
	return ok
}

// Clear resets the map.
func (vals Values) Clear() Values {
	clear(vals)
	return vals
}

// Clone returns a copy of the map.
func (vals Values) Clone() Values {
	var vals2 Values
	for k, vs := range vals {
		if vals2 == nil {
			vals2 = make(Values, len(vals))
		}
		vals2[k] = make([]string, len(vs))
		copy(vals2[k], vs)
	}
	return vals2
}

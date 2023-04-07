package common

import "github.com/ghettovoice/gosip/internal/utils"

// Values maps a string key to a list of string values.
// The keys in the map are case-insensitive.
// It is typically used to store URI's or header's parameters.
type Values map[string][]string

// Get returns values associated with the given key.
// If there are no values associated with the key, Get returns the empty slice.
func (vals Values) Get(key string) []string { return vals[utils.LCase(key)] }

func (vals Values) First(key string) string {
	v := vals[utils.LCase(key)]
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

func (vals Values) Last(key string) string {
	v := vals[utils.LCase(key)]
	if len(v) == 0 {
		return ""
	}
	return v[len(v)-1]
}

// Set sets the key to value. It replaces any existing values.
func (vals Values) Set(key, value string) Values {
	vals[utils.LCase(key)] = []string{value}
	return vals
}

func (vals Values) Append(key, value string) Values {
	key = utils.LCase(key)
	vals[key] = append(vals[key], value)
	return vals
}

func (vals Values) Prepend(key, value string) Values {
	key = utils.LCase(key)
	vals[key] = append([]string{value}, vals[key]...)
	return vals
}

// Del deletes the values associated with the key.
func (vals Values) Del(key string) Values {
	delete(vals, utils.LCase(key))
	return vals
}

// Has checks whether a given key is in the list.
func (vals Values) Has(key string) bool {
	_, ok := vals[utils.LCase(key)]
	return ok
}

// Clear resets the map.
func (vals Values) Clear() Values {
	clear(vals)
	return vals
}

// Clone returns copy of the map.
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

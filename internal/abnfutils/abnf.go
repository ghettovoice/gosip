// Package abnfutils provides utilities to work with ABNF grammar.
package abnfutils

import (
	"fmt"

	"github.com/ghettovoice/abnf"
)

// MustGetNode returns a pointer to the ABNF node with the given key.
func MustGetNode(n *abnf.Node, k string) *abnf.Node {
	sn := n.GetNode(k)
	if sn == nil {
		panic(fmt.Errorf(`ABNF node "%s" not found in "%s"`, k, n.Key))
	}
	return sn
}

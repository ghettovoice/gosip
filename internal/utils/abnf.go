package utils

import (
	"fmt"

	"github.com/ghettovoice/abnf"
)

func MustGetNode(n *abnf.Node, k string) *abnf.Node {
	sn := n.GetNode(k)
	if sn == nil {
		panic(fmt.Errorf(`ABNF node "%s" not found in "%s"`, k, n.Key))
	}
	return sn
}

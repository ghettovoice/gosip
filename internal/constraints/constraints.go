// Package constraints provides constraints for various types.
package constraints

// Byteseq represents a generic UTF-8 byte string.
type Byteseq interface {
	~string | ~[]byte
}

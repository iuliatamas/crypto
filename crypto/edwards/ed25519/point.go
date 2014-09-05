// Package ed25519 provides an optimized Go implementation of a
// Twisted Edwards curve that is isomorphic to Curve25519. For details see:
// http://ed25519.cr.yp.to/.
//
// This code is based on Adam Langley's Go port of the public domain,
// "ref10" implementation of the ed25519 signing scheme in C from SUPERCOP.
// It was generalized and extended to support full abstract group arithmetic
// by the Yale Decentralized/Distributed Systems (DeDiS) group.
//
// Due to the field element and group arithmetic optimizations
// described in the Ed25519 paper, this implementation generally performs
// extremely well, typically comparable to native C implementations.
// The tradeoff is that this code is completely specialized to a single curve.
// 
package ed25519

import (
	//"fmt"
	"hash"
	"errors"
	"crypto/aes"
	"encoding/hex"
	"crypto/cipher"
	"crypto/sha256"
	"dissent/crypto"
)


type point struct {
	ge extendedGroupElement
}


func (P *point) String() string {
	var b [32]byte
	P.ge.ToBytes(&b)
	return hex.EncodeToString(b[:])
}

func (P *point) Len() int {
	return 32
}

func (P *point) Encode() []byte {
	var b [32]byte
	P.ge.ToBytes(&b)
	return b[:]
}

func (P *point) Decode(b []byte) error {
	if !P.ge.FromBytes(b) {
		return errors.New("invalid Ed25519 curve point")
	}
	return nil
}

// Equality test for two Points on the same curve
func (P *point) Equal(P2 crypto.Point) bool {

	// XXX better to test equality without normalizing extended coords

	var b1,b2 [32]byte
	P.ge.ToBytes(&b1)
	P2.(*point).ge.ToBytes(&b2)
	for i := range(b1) {
		if b1[i] != b2[i] {
			return false
		}
	}
	return true
}

// Set point to be equal to P2.
func (P *point) Set(P2 crypto.Point) crypto.Point {
	P.ge = P2.(*point).ge
	return P
}

// Set to the neutral element, which is (0,1) for twisted Edwards curves.
func (P *point) Null() crypto.Point {
	P.ge.Zero()
	return P
}

// Set to the standard base point for this curve
func (P *point) Base(rand cipher.Stream) crypto.Point {
	if rand == nil {
		P.ge = baseext
	} else {
		for {
			P.Pick(nil, rand)	// pick a random point
			P.Mul(P, cofactor)	// multiply by Ed25519 cofactor
			if !P.Equal(pzero) {
				break		// got one
			}
			// retry
		}
	}
	return P
}

func (P *point) PickLen() int {
	// Reserve at least 8 most-significant bits for randomness,
	// and the least-significant 8 bits for embedded data length.
	// (Hopefully it's unlikely we'll need >=2048-bit curves soon.)
	return (255 - 8 - 8) / 8
}

func (P *point) Pick(data []byte, rand cipher.Stream) (crypto.Point, []byte) {

	// How many bytes to embed?
	dl := P.PickLen()
	if dl > len(data) {
		dl = len(data)
	}

	for {
		// Pick a random point, with optional embedded data
		var b [32]byte
		rand.XORKeyStream(b[:],b[:])
		if data != nil {
			b[0] = byte(dl)		// Encode length in low 8 bits
			copy(b[1:1+dl],data)	// Copy in data to embed
		}
		if P.ge.FromBytes(b[:]) {	// Try to decode
			return P,data[dl:]	// success
		}
		// invalid point, retry
	}
}

// Extract embedded data from a point group element
func (P *point) Data() ([]byte,error) {
	var b [32]byte
	P.ge.ToBytes(&b)
	dl := int(b[0])				// extract length byte
	if dl > P.PickLen() {
		return nil,errors.New("invalid embedded data length")
	}
	return b[1:1+dl],nil
}

func (P *point) Add(P1,P2 crypto.Point) crypto.Point {
	E1 := P1.(*point)
	E2 := P2.(*point)

	var t2 cachedGroupElement
	var r completedGroupElement

	E2.ge.ToCached(&t2)
	r.Add(&E1.ge, &t2)
	r.ToExtended(&P.ge)

	// XXX in this case better just to use general addition formula?

	return P
}

func (P *point) Sub(P1,P2 crypto.Point) crypto.Point {
	E1 := P1.(*point)
	E2 := P2.(*point)

	var t2 cachedGroupElement
	var r completedGroupElement

	E2.ge.ToCached(&t2)
	r.Sub(&E1.ge, &t2)
	r.ToExtended(&P.ge)

	// XXX in this case better just to use general addition formula?

	return P
}

// Find the negative of point A.
// For Edwards curves, the negative of (x,y) is (-x,y).
func (P *point) Neg(A crypto.Point) crypto.Point {
	P.ge.Neg(&A.(*point).ge)
	return P
}


// Multiply point p by scalar s using the repeated doubling method.
// XXX This is vartime; for our general-purpose Mul operator
// it would be far preferable for security to do this constant-time.
func (P *point) Mul(A crypto.Point, s crypto.Secret) crypto.Point {

	// Convert the scalar to fixed-length little-endian form.
	sb := s.(*crypto.ModInt).V.Bytes()
	shi := len(sb)-1
	var a [32]byte
	for i := range sb {
		a[shi-i] = sb[i]
	}

	if A == nil {
		geScalarMultBase(&P.ge, &a)
	} else {
		geScalarMult(&P.ge, &a, &A.(*point).ge)
		//geScalarMultVartime(&P.ge, &a, &A.(*point).ge)
	}

	return P
}


// Curve represents an Ed25519.
// There are no parameters and no initialization is required
// because it supports only this one specific curve.
type Curve struct {
}

// Return the name of the curve, "Ed25519".
func (c *Curve) String() string {
	return "Ed25519"
}

// Returns 32, the size in bytes of an encoded Secret for the Ed25519 curve.
func (c *Curve) SecretLen() int {
	return 32
}

// Create a new Secret for the Ed25519 curve.
func (c *Curve) Secret() crypto.Secret {
	return crypto.NewModInt(0, order)
}

// Returns 32, the size in bytes of an encoded Point on the Ed25519 curve.
func (c *Curve) PointLen() int {
	return 32
}

// Create a new Point on the Ed25519 curve.
func (c *Curve) Point() crypto.Point {
	P := new(point)
	//P.c = c
	return P
}


type suite struct {
	Curve
} 

// XXX non-NIST ciphers?

// SHA256 hash function
func (s *suite) HashLen() int { return sha256.Size }
func (s *suite) Hash() hash.Hash {
	return sha256.New()
}

// AES128-CTR stream cipher
func (s *suite) KeyLen() int { return 16 }
func (s *suite) Stream(key []byte) cipher.Stream {
	aes, err := aes.NewCipher(key)
	if err != nil {
		panic("can't instantiate AES: " + err.Error())
	}
	iv := make([]byte,16)
	return cipher.NewCTR(aes,iv)
}

// Ciphersuite based on AES-128, SHA-256, and the Ed25519 curve.
func newAES128SHA256Ed25519() crypto.Suite {
	suite := new(suite)
	return suite
}


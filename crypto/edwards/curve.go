package edwards

import (
	"fmt"
	"errors"
	"math/big"
	"crypto/cipher"
	"dissent/crypto"
)

var zero = big.NewInt(0)
var one = big.NewInt(1)


// Byte-reverse src into dst,
// so that src[0] goes into dst[len-1] and vice versa.
// dst and src may be the same slice but otherwise must not overlap.
// XXX this probably belongs in a utils package somewhere.
func reverse(dst,src []byte) []byte {
	l := len(dst)
	if len(src) != l {
		panic("different-length slices passed to reverse")
	}
	if &dst[0] == &src[0] {		// in-place
		for i := 0; i < l/2; i++ {
			t := dst[i]
			dst[i] = dst[l-1-i]
			dst[l-1-i] = t
		}
	} else {
		for i := 0; i < l; i++ {
			dst[i] = src[l-1-i]
		}
	}
	return dst
}


// Generic "abstract base class" for Edwards curves,
// embodying functionality independent of internal Point representation.
type curve struct {
	Param			// Twisted Edwards curve parameters
	zero,one crypto.ModInt	// Constant ModInts with correct modulus
	cofact crypto.ModInt	// Cofactor as a ModInt
	a,d crypto.ModInt	// Curve equation parameters as ModInts
}

// Returns the size in bytes of an encoded Secret for this curve.
func (c *curve) SecretLen() int {
	return (c.R.BitLen() + 7) / 8
}

// Create a new Secret for this curve.
func (c *curve) Secret() crypto.Secret {
	return crypto.NewModInt(0, &c.R)
}

// Returns the size in bytes of an encoded Point on this curve.
// Uses compressed representation consisting of the y-coordinate
// and only the sign bit of the x-coordinate.
func (c *curve) PointLen() int {
	return (c.P.BitLen() + 7 + 1) / 8
}

// Initialize a twisted Edwards curve with given parameters.
func (c *curve) init(p *Param) *curve {
	c.Param = *p

	// Edwards curve parameters as ModInts for convenience
	c.a.Init(&p.A,&p.P)
	c.d.Init(&p.D,&p.P)

	// Cofactor, for random point generation
	c.cofact.Init(&p.S,&p.P)

	// Useful ModInt constants for this curve
	c.zero.Init64(0, &c.P)
	c.one.Init64(1, &c.P)

	return c
}

// Test the sign of an x or y coordinate.
// We use the least-significant bit of the coordinate as the sign bit.
func (c *curve) coordSign(i *crypto.ModInt) uint {
	return i.V.Bit(0)
}

// Convert a point to string representation.
func (c *curve) pointString(x,y *crypto.ModInt) string {
	return fmt.Sprintf("(%s,%s)", x.String(), y.String())
}

// Encode an Edwards curve point.
// We use little-endian encoding for consistency with Ed25519.
func (c *curve) encodePoint(x,y *crypto.ModInt) []byte {

	// Encode the y-coordinate
	b := y.Encode()

	// Encode the sign of the x-coordinate.
	if y.M.BitLen() & 7 == 0 {
		// No unused bits at the top of y-coordinate encoding,
		// so we must prepend a whole byte.
		b = append(make([]byte,1), b...)
	}
	if c.coordSign(x) != 0 {
		b[0] |= 0x80
	}

	// Convert to little-endian
	reverse(b,b)
	return b
}

// Decode an Edwards curve point into the given x,y coordinates.
func (c *curve) decodePoint(bb []byte, x,y *crypto.ModInt) error {

	// Convert from little-endian
	b := make([]byte, len(bb))
	reverse(b,bb)

	// Extract the sign of the x-coordinate
	xsign := uint(b[0] >> 7)
	b[0] &^= 0x80

	// Extract the y-coordinate
	y.V.SetBytes(b)

	// Compute the corresponding x-coordinate
	if !c.solveForX(x,y) {
		return errors.New("invalid elliptic curve point")
	}
	if c.coordSign(x) != xsign {
		x.Neg(x)
	}

	return nil
}

// Given a y-coordinate, solve for the x-coordinate on the curve,
// using the characteristic equation rewritten as:
//
//	x^2 = (1 - y^2)/(a - d*y^2)
//
// Returns true on success,
// false if there is no x-coordinate corresponding to the chosen y-coordinate.
//
func (c *curve) solveForX(x,y *crypto.ModInt) bool {
	var yy,t1,t2 crypto.ModInt

	yy.Mul(y,y)				// yy = y^2
	t1.Sub(&c.one,&yy)			// t1 = 1 - y^-2
	t2.Mul(&c.d,&yy).Sub(&c.a,&t2)		// t2 = a - d*y^2
	t2.Div(&t1,&t2)				// t2 = x^2
	return x.Sqrt(&t2)			// may fail if not a square
}

// Test if a supposed point is on the curve,
// by checking the characteristic equation for Edwards curves:
//
//	a*x^2 + y^2 = 1 + d*x^2*y^2
//
func (c *curve) onCurve(x,y *crypto.ModInt) bool {
	var xx,yy,l,r crypto.ModInt

	xx.Mul(x,x)				// xx = x^2
	yy.Mul(y,y)				// yy = y^2

	l.Mul(&c.a,&xx).Add(&l,&yy)		// l = a*x^2 + y^2
	r.Mul(&c.d,&xx).Mul(&r,&yy).Add(&c.one,&r)
						// r = 1 + d*x^2*y^2
	return l.Equal(&r)
}

// Return number of bytes that can be embedded into points on this curve.
func (c *curve) pickLen() int {
	// Reserve at least 8 most-significant bits for randomness,
	// and the least-significant 8 bits for embedded data length.
	// (Hopefully it's unlikely we'll need >=2048-bit curves soon.)
	return (c.P.BitLen() - 8 - 8) / 8
}

// Pick a [pseudo-]random curve point with optional embedded data,
// filling in the point's x,y coordinates
// and returning any remaining data not embedded.
func (c *curve) pickPoint(data []byte, rand cipher.Stream,
			x,y *crypto.ModInt) []byte {

	// How much data to embed?
	dl := c.pickLen()
	if dl > len(data) {
		dl = len(data)
	}

	// Retry until we find a valid point
	for {
		// Get random bits the size of a compressed Point encoding,
		// in which the topmost bit is reserved for the x-coord sign.
		l := c.PointLen()
		b := make([]byte, l)
		rand.XORKeyStream(b,b)		// Interpret as little-endian
		if data != nil {
			b[0] = byte(dl)		// Encode length in low 8 bits
			copy(b[1:1+dl],data)	// Copy in data to embed
		}
		reverse(b,b)			// Convert to big-endian form

		xsign := b[0] >> 7		// save x-coordinate sign bit
		b[0] &^= 0xff << uint(c.P.BitLen() & 7)	// clear high bits

		y.M = &c.P			// set y-coordinate
		y.SetBytes(b)

		if !c.solveForX(x,y) {	// Find a corresponding x-coordinate
			continue	// none, retry
		}

		// Pick a random sign for the x-coordinate
		if c.coordSign(x) != uint(xsign) {
			x.Neg(x)
		}
		if !c.onCurve(x,y) {
			panic("Pick generated a bad point")
		}

		return data[dl:]
	}
}

// Pick a [pseudo-]random base point of prime order.
func (c *curve) pickBase(rand cipher.Stream, p,null crypto.Point) {

	for {
		p.Pick(nil, rand)	// pick a random point
		p.Mul(p, &c.cofact)	// multiply by cofactor
		if !p.Equal(null) {
			break			// got one
		}
		// retry
	}
}

// Extract embedded data from a point group element,
// or an error if embedded data is invalid or not present.
func (c *curve) data(x,y *crypto.ModInt) ([]byte,error) {
	b := c.encodePoint(x,y)
	dl := int(b[0])
	if dl > c.pickLen() {
		return nil,errors.New("invalid embedded data length")
	}
	return b[1:1+dl],nil
}


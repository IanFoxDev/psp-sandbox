// Package ids generates object ids such as pay_01J9Z3K8Q2W5. With a seed the
// sequence is the same on every run, which keeps test output stable.
package ids

import (
	"hash/fnv"
	"math/rand/v2"
	"sync"
)

// Crockford base32 without I, L, O and U.
const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

const length = 12

// Generator is safe for concurrent use.
type Generator struct {
	mu  sync.Mutex
	rng *rand.Rand
}

// New returns a generator. An empty seed gives random ids.
func New(seed string) *Generator {
	return &Generator{rng: Rand(seed, "ids")}
}

// Next returns prefix + "_" + 12 characters.
func (g *Generator) Next(prefix string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	b := make([]byte, 0, len(prefix)+1+length)
	b = append(b, prefix...)
	b = append(b, '_')
	for range length {
		b = append(b, alphabet[g.rng.IntN(len(alphabet))])
	}
	return string(b)
}

// Rand returns a source of randomness for one purpose (stream). With the same
// seed and stream it produces the same numbers. It is not safe for concurrent use.
func Rand(seed, stream string) *rand.Rand {
	if seed == "" {
		return rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	}
	h := fnv.New64a()
	h.Write([]byte(seed))
	a := h.Sum64()
	h.Write([]byte{0})
	h.Write([]byte(stream))
	return rand.New(rand.NewPCG(a, h.Sum64()))
}

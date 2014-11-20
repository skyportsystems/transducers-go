package transduce

import (
	"fmt"
	"math/rand"
)

// This is an outer piece, so doesn't need a type - use em how you want
// type Materializer func(Transducer, Iterator)

// Transducers are an interface, but...
type Transducer interface {
	Transduce(Reducer) Reducer
	Flush()
}

// We also provide an easy way to express them as pure functions
type TransducerFunc func(Reducer) Reducer

func (f TransducerFunc) Transduce(r Reducer) Reducer {
	return f(r)
}

type Mapper func(interface{}) interface{}
type Filterer func(interface{}) bool

const dbg = true

func fml(v ...interface{}) {
	if dbg {
		fmt.Println(v)
	}
}

// Exploders transform a value of some type into a stream of values.
// No guarantees about the relationship between the type of input and output;
// output may be a collection of the input type, or may not.
type Exploder func(interface{}) ValueStream

type Reducer func(accum interface{}, value interface{}) (result interface{})

func Sum(accum interface{}, val interface{}) (result interface{}) {
	return accum.(int) + val.(int)
}

func sum(vs ValueStream) (total int) {
	vs.Each(func(value interface{}) {
		fml("SUM: total", total)
		total += value.(int)
	})
	fml("SUM: final total", total)

	return
}

// Basic Mapper function (increments by 1)
func inc(value interface{}) interface{} {
	return value.(int) + 1
}

// Basic Filterer function (true if even)
func even(value interface{}) bool {
	return value.(int)%2 == 0
}

// Dumb little thing to emulate clojure's range behavior
func t_range(l int) []int {
	slice := make([]int, l)

	for i := 0; i < l; i++ {
		slice[i] = i
	}

	return slice
}

// Flattens arbitrarily deep datastructures into a single ValueStream.
func Flatten(value interface{}) ValueStream {
	switch v := value.(type) {
	case ValueStream:
		return flattenValueStream(v)
	case []interface{}:
		// TODO maybe detect ValueStreams here, too, but probably better to just be consistent
		return ValueSlice(v).AsStream()
	case []int:
		return MakeReduce(v)
	case int, interface{}:
		var done bool
		// create single-eleement value stream
		return func() (interface{}, bool) {
			if done {
				return nil, true
			} else {
				done = true
				return v, false
			}
		}
	default:
		panic("not supported")
	}
}

// Wraps t_range into a ValueStream
func Range(limit interface{}) ValueStream {
	// lazy and inefficient to use MakeReduce here, do it directly
	return MakeReduce(t_range(limit.(int)))
}

// Bind a function to the given collection that will allow traversal for reducing
func MakeReduce(collection interface{}) ValueStream {
	// If the structure already provides a reducing method, just return that.
	if c, ok := collection.(Streamable); ok {
		return c.AsStream()
	}

	switch c := collection.(type) {
	case []int:
		return iteratorToValueStream(&IntSliceIterator{slice: c})
	default:
		panic("not supported...yet")
	}
}

func Identity(accum interface{}, value interface{}) interface{} {
	return value
}

func Seq(vs ValueStream, init []int, tlist ...Transducer) []int {
	fml(tlist)
	// Final reducing func - append to the list
	t := Append(Identity)

	// Walk backwards through transducer list to assemble in
	// correct order
	for i := len(tlist) - 1; i >= 0; i-- {
		fml(tlist[i])
		t = tlist[i].Transduce(t)
	}

	var v interface{}
	var done bool
	var ret interface{} = init

	for {
		v, done = vs()
		if done {
			break
		}

		fml("Main loop:", v)
		// weird that we do nothing here
		ret = t(ret, v.(int))
	}

	return ret.([]int)
}

func Map(f Mapper) TransducerFunc {
	return func(r Reducer) Reducer {
		return func(accum interface{}, value interface{}) interface{} {
			fml("MAP: accum is", accum, "value is", value)
			return r(accum, f(value).(int))
		}
	}
}

func Filter(f Filterer) TransducerFunc {
	return func(r Reducer) Reducer {
		return func(accum interface{}, value interface{}) interface{} {
			fml("FILTER: accum is", accum, "value is", value)
			if f(value) {
				return r(accum, value)
			} else {
				return accum
			}
		}
	}
}

func Append(r Reducer) Reducer {
	return func(accum interface{}, value interface{}) interface{} {
		fml("APPEND: Appending", value, "onto", accum)
		switch v := r(accum, value).(type) {
		case []int:
			return append(accum.([]int), v...)
		case int:
			return append(accum.([]int), v)
		case ValueStream:
			flattenValueStream(v).Each(func(value interface{}) {
				fml("APPEND: *actually* appending ", value, "onto", accum)
				accum = append(accum.([]int), value.(int))
			})
			return accum
		default:
			panic("not supported")
		}
	}
}

// Mapcat first runs an exploder, then 'concats' results by
// passing each individual value along to the next transducer
// in the stack.
func Mapcat(f Exploder) TransducerFunc {
	return func(r Reducer) Reducer {
		return func(accum interface{}, value interface{}) interface{} {
			fml("MAPCAT: Processing explode val:", value)
			stream := f(value)

			var v interface{}
			var done bool

			for { // <-- the *loop* is the 'cat'
				v, done = stream()
				if done {
					break
				}
				fml("MAPCAT: Calling next t on val:", v, "accum is:", accum)

				accum = r(accum, v)
			}

			return accum
		}
	}
}

// Dedupe is a particular type of filter, but its statefulness
// means we need to treat it differently and can't reuse Filter
func Dedupe() TransducerFunc {
	// Statefulness is encapsulated in the transducer function - when
	// a materializing function calls the transducer, it produces a
	// fresh state that lives only as long as that run.
	return func(r Reducer) Reducer {
		// TODO Slice is fine for prototype, but should replace with
		// type-appropriate search tree later
		seen := make([]interface{}, 0)
		return func(accum interface{}, value interface{}) interface{} {
			for _, v := range seen {
				if value == v {
					return accum
				}
			}

			seen = append(seen, value)
			return r(accum, value)
		}
	}
}

// Condense the traversed collection by partitioning it into
// chunks of []interface{} of the given length.
//
// Here's one place we sorely feel the lack of algebraic types.
//
// Stateful.
func Chunk(length int) TransducerFunc {
	if length < 1 {
		panic("chunks must be at least one element in size")
	}

	return func(r Reducer) Reducer {
		// TODO look into most memory-savvy ways of doing this
		coll := make(ValueSlice, length, length)
		var count int
		return func(accum interface{}, value interface{}) interface{} {
			fml("CHUNK: Chunk count: ", count, "coll contents: ", coll)
			coll[count] = value
			count++

			if count == length {
				count = 0
				newcoll := make(ValueSlice, length, length)
				copy(newcoll, coll)
				fml("CHUNK: passing val to next td:", coll)
				return r(accum, newcoll.AsStream())
			} else {
				return accum
			}
		}
	}
}

// Condense the traversed collection by partitioning it into chunks,
// represented by ValueStreams. A new contiguous stream is created every time
// the injected filter function returns true.
func ChunkBy(f Filterer) TransducerFunc {
	return func(r Reducer) Reducer {
		var coll []interface{}
		return func(accum interface{}, value interface{}) interface{} {
			fml("CHUNKBY: Chunk size: ", len(coll), "coll contents: ", coll)
			var vals ValueSlice
			if vs, ok := value.(ValueStream); ok {
				fml("CHUNKBY: operating on ValueStream")
				// TODO this SUUUUUCKS, we have to duplicate the stream
				// the fact that the logic splits like this is indicative of a deeper problem
				vs.Each(func(v interface{}) {
					vals = append(vals, v)
				})

				fml("CHUNKBY: collected vals:", vals)

				if !f(vals.AsStream()) {
					fml("CHUNKBY: chunk unfinished; appending these vals to coll:", vals)
					coll = append(coll, vals.AsStream())
				} else {
					fml("CHUNKBY: passing value streams to next td:", coll)
					accum = r(accum, coll)
					coll = nil
					coll = append(coll, vals.AsStream())
				}
			} else {
				fml("CHUNKBY: operating on non-ValueStream")
				if !f(value) {
					fml("CHUNKBY: chunk unfinished; appending this val to coll:", value)
					coll = append(coll, value)
				} else {
					fml("CHUNKBY: passing coll to next td:", coll)
					accum = r(accum, coll)
					coll = nil // TODO erm...correct way to zero out a slice?
					coll = append(coll, value)
				}
			}

			return accum
		}
	}
}

// Passes the received value along to the next transducer, with the
// given probability.
func RandomSample(ρ float64) TransducerFunc {
	if ρ < 0.0 || ρ > 1.0 {
		panic("ρ must be in the range [0.0,1.0].")
	}

	return Filter(func(_ interface{}) bool {
		return rand.Float64() < ρ
	})
}

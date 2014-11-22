package transduce

import (
	"fmt"
	"math/rand"
)

// The master signature: a reducing step function.
type Reducer func(accum interface{}, value interface{}) (result interface{}, terminate bool)

// Transducers are an interface, but...
type Transducer interface {
	Transduce(ReduceStep) ReduceStep
}

type TransducerFunc func(ReduceStep) ReduceStep

func (f TransducerFunc) Transduce(r ReduceStep) ReduceStep {
	return f(r)
}

type ReduceStep interface {
	// The primary reducing step function, called during normal operation.
	Reduce(accum interface{}, value interface{}) (result interface{}, terminate bool) // Reducer

	// Complete is called when the input has been exhausted; stateful transducers
	// should flush any held state (e.g. values awaiting a full chunk) through here.
	Complete(accum interface{}) (result interface{})
}

type Mapper func(value interface{}) interface{}
type IndexedMapper func(index int, value interface{}) interface{}
type Filterer func(interface{}) bool

const dbg = false

func fml(v ...interface{}) {
	if dbg {
		fmt.Println(v)
	}
}

// Exploders transform a value of some type into a stream of values.
// No guarantees about the relationship between the type of input and output;
// output may be a collection of the input type, or may not.
type Exploder func(interface{}) ValueStream

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
	case []interface{}:
		return ValueSlice(c).AsStream()
	default:
		panic("not supported...yet")
	}
}

func Identity(accum interface{}, value interface{}) (interface{}, bool) {
	return value, false
}

type map_r struct {
	reducerBase
	f Mapper
}

func (r map_r) Reduce(accum interface{}, value interface{}) (interface{}, bool) {
	fml("MAP: accum is", accum, "value is", value)
	return r.next.Reduce(accum, r.f(value))
}

func Map(f Mapper) TransducerFunc {
	return func(r ReduceStep) ReduceStep {
		return map_r{reducerBase{r}, f}
	}
}

type filter struct {
	reducerBase
	f Filterer
}

func (r filter) Reduce(accum interface{}, value interface{}) (interface{}, bool) {
	fml("FILTER: accum is", accum, "value is", value)
	if r.f(value) {
		return r.next.Reduce(accum, value)
	} else {
		return accum, false
	}
}

func Filter(f Filterer) TransducerFunc {
	return func(r ReduceStep) ReduceStep {
		return filter{reducerBase{r}, f}
	}
}

// for append operations at the bottom of a transducer stack
type append_bottom struct{}

func (r append_bottom) Reduce(accum interface{}, value interface{}) (interface{}, bool) {
	fml("APPEND: Appending", value, "onto", accum)
	switch v := value.(type) {
	case []int:
		return append(accum.([]int), v...), false
	case int:
		return append(accum.([]int), v), false
	case ValueStream:
		flattenValueStream(v).Each(func(value interface{}) {
			fml("APPEND: *actually* appending ", value, "onto", accum)
			accum = append(accum.([]int), value.(int))
		})
		return accum, false
	default:
		panic("not supported")
	}
}

func (r append_bottom) Complete(accum interface{}) interface{} {
	return accum
}

func Append() ReduceStep {
	return append_bottom{}
}

type mapcat struct {
	reducerBase
	f Exploder
}

func (r mapcat) Reduce(accum interface{}, value interface{}) (interface{}, bool) {
	fml("MAPCAT: Processing explode val:", value)
	stream := r.f(value)

	var v interface{}
	var done, terminate bool

	for { // <-- the *loop* is the 'cat'
		v, done = stream()
		if done {
			break
		}
		fml("MAPCAT: Calling next t on val:", v, "accum is:", accum)

		accum, terminate = r.next.Reduce(accum, v)
		if terminate {
			break
		}
	}

	return accum, terminate
}

// Mapcat first runs an exploder, then 'concats' results by
// passing each individual value along to the next transducer
// in the stack.
func Mapcat(f Exploder) TransducerFunc {
	return func(r ReduceStep) ReduceStep {
		return mapcat{reducerBase{r}, f}
	}
}

type dedupe struct {
	reducerBase
	// TODO Slice is fine for prototype, but should replace with type-appropriate
	// search tree later
	seen ValueSlice
}

func (r *dedupe) Reduce(accum interface{}, value interface{}) (interface{}, bool) {
	fml("DEDUPE: have seen", r.seen, "accum is", accum, "value is", value)
	for _, v := range r.seen {
		if value == v {
			return accum, false
		}
	}

	r.seen = append(r.seen, value)
	return r.next.Reduce(accum, value)
}

// Dedupe keeps track of values that have passed through it during this
// transduction process and drops any duplicates.
//
// Simple equality (==) is used for comparison - will blow up on non-hashable
// datastructures (maps, slices, channels)!
func Dedupe() TransducerFunc {
	return func(r ReduceStep) ReduceStep {
		return &dedupe{reducerBase{r}, make([]interface{}, 0)}
	}
}

// Condense the traversed collection by partitioning it into
// chunks of []interface{} of the given length.
//
// Here's one place we sorely feel the lack of algebraic types.
func Chunk(length int) TransducerFunc {
	if length < 1 {
		panic("chunks must be at least one element in size")
	}

	return func(r ReduceStep) ReduceStep {
		// TODO look into most memory-savvy ways of doing this
		return &chunk{length: length, coll: make(ValueSlice, length, length), next: r}
	}
}

// Chunk is stateful, so it's handled with a struct instead of a pure function
type chunk struct {
	length    int
	count     int
	terminate bool
	coll      ValueSlice
	next      ReduceStep
}

func (t *chunk) Reduce(accum interface{}, value interface{}) (interface{}, bool) {
	fml("CHUNK: Chunk count: ", t.count, "coll contents: ", t.coll)
	t.coll[t.count] = value
	t.count++ // TODO atomic

	if t.count == t.length {
		t.count = 0
		newcoll := make(ValueSlice, t.length, t.length)
		copy(newcoll, t.coll)
		fml("CHUNK: passing val to next td:", t.coll)
		accum, t.terminate = t.next.Reduce(accum, newcoll.AsStream())
		return accum, t.terminate
	} else {
		return accum, false
	}
}

func (t *chunk) Complete(accum interface{}) interface{} {
	fml("CHUNK: Completing...")
	// if there's a partially-completed chunk, send it through reduction as-is
	if t.count != 0 && !t.terminate {
		fml("CHUNK: Leftover values found, passing coll to next td:", t.coll[:t.count])
		// should be fine to send the original, we know we're done
		accum, t.terminate = t.next.Reduce(accum, t.coll[:t.count].AsStream())
	}

	return t.next.Complete(accum)
}

// Condense the traversed collection by partitioning it into chunks,
// represented by ValueStreams. A new contiguous stream is created every time
// the injected filter function returns true.
func ChunkBy(f Filterer) TransducerFunc {
	if f == nil {
		panic("cannot provide nil function pointer to ChunkBy")
	}

	return func(r ReduceStep) ReduceStep {
		// TODO look into most memory-savvy ways of doing this
		return &chunkBy{chunker: f, coll: make(ValueSlice, 0), next: r}
	}
}

type chunkBy struct {
	chunker   Filterer
	coll      ValueSlice
	next      ReduceStep
	terminate bool
}

func (t *chunkBy) Reduce(accum interface{}, value interface{}) (interface{}, bool) {
	fml("CHUNKBY: Chunk size: ", len(t.coll), "coll contents: ", t.coll)
	if vs, ok := value.(ValueStream); ok {
		var vals ValueSlice
		fml("CHUNKBY: operating on ValueStream")
		// TODO this SUUUUUCKS, we have to duplicate the stream
		// TODO the fact that the logic splits like this is indicative of a deeper problem
		vs.Each(func(v interface{}) {
			vals = append(vals, v)
		})

		fml("CHUNKBY: collected vals:", vals)

		// TODO this is not chunkby...it should make a new group every time a new val comes back
		if !t.chunker(vals.AsStream()) {
			fml("CHUNKBY: chunk unfinished; appending these vals to coll:", vals)
			t.coll = append(t.coll, vals.AsStream())
		} else {
			fml("CHUNKBY: passing value streams to next td:", t.coll)
			accum, t.terminate = t.next.Reduce(accum, t.coll.AsStream())
			t.coll = nil
			t.coll = append(t.coll, vals.AsStream())
		}
	} else {
		fml("CHUNKBY: operating on non-ValueStream")
		if !t.chunker(value) {
			fml("CHUNKBY: chunk unfinished; appending this val to coll:", value)
			t.coll = append(t.coll, value)
		} else {
			fml("CHUNKBY: passing coll to next td:", t.coll)
			accum, t.terminate = t.next.Reduce(accum, t.coll.AsStream())
			t.coll = nil // TODO erm...correct way to zero out a slice?
			t.coll = append(t.coll, value)
		}
	}

	return accum, t.terminate
}

func (t *chunkBy) Complete(accum interface{}) interface{} {
	fml("CHUNKBY: Completing...")
	// if there's a partially-completed chunk, send it through reduction as-is
	if len(t.coll) != 0 && !t.terminate {
		fml("CHUNKBY: Leftover values found, passing coll to next td:", t.coll)
		accum, t.terminate = t.next.Reduce(accum, t.coll.AsStream())
	}

	return t.next.Complete(accum)
}

type randomSample struct {
	filter
}

// Passes the received value along to the next transducer, with the
// given probability.
func RandomSample(ρ float64) TransducerFunc {
	if ρ < 0.0 || ρ > 1.0 {
		panic("ρ must be in the range [0.0,1.0].")
	}

	return func(r ReduceStep) ReduceStep {
		return randomSample{filter{reducerBase{r}, func(_ interface{}) bool {
			//panic("oh shit")
			return rand.Float64() < ρ
		}}}
	}
}

type takeNth struct {
	filter
}

// TakeNth takes every nth element to pass through it, discarding the remainder.
func TakeNth(n int) TransducerFunc {
	var count int

	return func(r ReduceStep) ReduceStep {
		return takeNth{filter{reducerBase{r}, func(_ interface{}) bool {
			// TODO check how stack trace looks when we don't reimplement Reduce
			count++ // TODO atomic
			return count%n == 0
		}}}
	}
}

type keep struct {
	reducerBase
	f Mapper
}

func (r keep) Reduce(accum interface{}, value interface{}) (interface{}, bool) {
	fml("KEEP: accum is", accum, "value is", value)
	nv := r.f(value)
	if nv != nil {
		return r.next.Reduce(accum, nv)
	}
	fml("KEEP: discarding nil")
	return accum, false
}

// Keep calls the provided mapper, then discards any nil value returned from the mapper.
func Keep(f Mapper) TransducerFunc {
	return func(r ReduceStep) ReduceStep {
		return keep{reducerBase{r}, f}
	}
}

type keepIndexed struct {
	reducerBase
	count int
	f     IndexedMapper
}

func (r *keepIndexed) Reduce(accum interface{}, value interface{}) (interface{}, bool) {
	fml("KEEPINDEXED: accum is", accum, "value is", value, "count is", r.count)
	nv := r.f(r.count, value)
	r.count++ // TODO atomic

	if nv != nil {
		return r.next.Reduce(accum, nv)
	}

	fml("KEEPINDEXED: discarding nil")
	return accum, false

}

// KeepIndexed calls the provided indexed mapper, then discards any nil value
// return from the mapper.
func KeepIndexed(f IndexedMapper) TransducerFunc {
	return func(r ReduceStep) ReduceStep {
		return &keepIndexed{reducerBase{r}, 0, f}
	}
}

type replace struct {
	reducerBase
	pairs map[interface{}]interface{}
}

func (r replace) Reduce(accum interface{}, value interface{}) (interface{}, bool) {
	if v, exists := r.pairs[value]; exists {
		fml("REPLACE: match found, replacing", value, "with", v)
		return r.next.Reduce(accum, v)
	}
	fml("REPLACE: no match, passing along", value)
	return r.next.Reduce(accum, value)
}

// Given a map of replacement value pairs, will replace any value moving through
// that has a key in the map with the corresponding value.
func Replace(pairs map[interface{}]interface{}) TransducerFunc {
	return func(r ReduceStep) ReduceStep {
		return replace{reducerBase{r}, pairs}
	}
}

type take struct {
	reducerBase
	max   uint
	count uint
}

func (r *take) Reduce(accum interface{}, value interface{}) (interface{}, bool) {
	r.count++ // TODO atomic
	fml("TAKE: processing item", r.count, "of", r.max, "; accum is", accum, "value is", value)
	if r.count < r.max {
		return r.next.Reduce(accum, value)
	}
	// should NEVER be called again after this. add a panic branch?
	accum, _ = r.next.Reduce(accum, value)
	fml("TAKE: reached final item, returning terminator")
	return accum, true
}

// Take specifies a maximum number of values to receive, after which it will
// terminate the transducing process.
func Take(max uint) TransducerFunc {
	return func(r ReduceStep) ReduceStep {
		return &take{reducerBase{r}, max, 0}
	}
}

type takeWhile struct {
	reducerBase
	f Filterer
}

func (r takeWhile) Reduce(accum interface{}, value interface{}) (interface{}, bool) {
	fml("TAKEWHILE: accum is", accum, "value is", value)
	if !r.f(value) {
		fml("TAKEWHILE: filtering func returned false, terminating")
		return accum, true
	}
	return r.next.Reduce(accum, value)
}

// TakeWhile accepts values until the injected filterer function returns false.
func TakeWhile(f Filterer) TransducerFunc {
	return func(r ReduceStep) ReduceStep {
		return takeWhile{reducerBase{r}, f}
	}
}

type drop struct {
	reducerBase
	min   uint
	count uint
}

func (r *drop) Reduce(accum interface{}, value interface{}) (interface{}, bool) {
	fml("DROP: processing item", r.count+1, ", min is", r.min, "; accum is", accum, "value is", value)
	if r.count < r.min {
		// Increment inside so no mutation after threshold is met
		r.count++ // TODO atomic
		return accum, false
	}
	// should NEVER be called again after this. add a panic branch?
	return r.next.Reduce(accum, value)
}

// Drop specifies a number of values to initially ignore, after which it will
// let everything through unchanged.
func Drop(min uint) TransducerFunc {
	return func(r ReduceStep) ReduceStep {
		return &drop{reducerBase{r}, min, 0}
	}
}

type dropWhile struct {
	reducerBase
	f        Filterer
	accepted bool
}

func (r *dropWhile) Reduce(accum interface{}, value interface{}) (interface{}, bool) {
	fml("DROPWHILE: accum is", accum, "value is", value)
	if !r.accepted {
		if !r.f(value) {
			fml("DROPWHILE: filtering func returned false, accepting from now on")
			r.accepted = true
		} else {
			return accum, false
		}
	}
	return r.next.Reduce(accum, value)
}

// DropWhile drops values until the injected filterer function returns false.
func DropWhile(f Filterer) TransducerFunc {
	return func(r ReduceStep) ReduceStep {
		return &dropWhile{reducerBase{r}, f, false}
	}
}

type remove struct {
	filter
}

// Remove drops items when the injected filterer function returns true.
//
// It is the inverse of Filter.
func Remove(f Filterer) TransducerFunc {
	return func(r ReduceStep) ReduceStep {
		return remove{filter{reducerBase{r}, func(value interface{}) bool {
			return !f(value)
		}}}
	}
}

# Transducers for Go

[![Build Status](https://travis-ci.org/sdboyer/transducers-go.svg?branch=master)](https://travis-ci.org/sdboyer/transducers-go)

This is an implementation of transducers, a concept from [Clojure](http://clojure.org), for Go.

Transducers can be tricky to understand with just an abstract description, but here it is:

> Transducers are a composable way to build reusable algorithmic transformations.

Transducers were introduced in Clojure for a sorta-similar reason that `range` exists in Go: having one way of writing element-wise operations on channels *and* other collection structures (though that's just the tip of the iceberg).

I'm honestly not sure if I these are a good idea for Go. I've written this library as an exploratory experiment in their utility for Go, and would love feedback.

## What Transducers are

There's a lot out there already, and I don't want to duplicate that here. Here are some bullets to quickly orient you:

* They're a framework for performing structured transformations on streams of values. If you're familiar with pipeline processing, kinda like that.
* They separate the WAY a transformation is run (concurrently or not, eagerly or lazily) from WHAT the transformation is, while also decomposing HOW the tranformation works into its smallest possible reusable parts.
* The **whole entire thing** is built on a single observation: you can express every possible collection-type operation (map, filter, etc.) as in the form of a reduce operation.
* Clojure is a Lisp. Lisp, as in, "list processing". We have different-looking list primitives in Go (slices/arrays, basically). This difference is much of why transducers can be so fundamental for Clojure, but may seem foreign (though not *necessarily* wrong) in Go.

Beyond that, here's some resources (mostly in Clojure):

* If Clojure makes your eyes cross, here's a writeup in [Javascript](http://phuu.net/2014/08/31/csp-and-transducers.html), and one in [PHP](https://github.com/mtdowling/transducers.php). Cognitect has also [implemented transducers](http://cognitect-labs.github.io/) in Python, Javascript, Ruby, and Java.
* Rich Hickey's [StrangeLoop talk](https://www.youtube.com/watch?v=6mTbuzafcII) introducing transducers (and his recent [ClojureConj talk](https://www.youtube.com/watch?v=4KqUvG8HPYo))
* The [Clojure docs](http://clojure.org/transducers) page for transducers
* [Some](https://gist.github.com/ptaoussanis/e537bd8ffdc943bbbce7) [high-level](https://bendyworks.com/transducers-clojures-next-big-idea/) [summaries](http://thecomputersarewinning.com/post/Transducers-Are-Fundamental/) of transducers
* Some [examples](http://ianrumford.github.io/blog/2014/08/08/Some-trivial-examples-of-using-Clojure-Transducers/) of [uses](http://matthiasnehlsen.com/blog/2014/10/06/Building-Systems-in-Clojure-2/) for transducers...mostly just toy stuff
* A couple [blog](http://blog.podsnap.com/ducers2.html) [posts](http://conscientiousprogrammer.com/blog/2014/08/07/understanding-cloure-transducers-through-types/) examining type issues with transducers

## Pudding <- Proof

I'm calling this proof of concept "done" because [it can pretty much replicate](http://godoc.org/github.com/sdboyer/transducers-go#ex-package--ClojureParity) (expand the ClojureParity example) a [thorough demo case](https://gist.github.com/sdboyer/9fca652f492257f35a41) Rich Hickey put out there.

Here's some quick eye candy, though:

```go
// dot import for brevity, remember this is a nono
import . "github.com/sdboyer/transducers-go"

func main() {
	// To make things work, we need four things (definitions in glossary):
	// 1) an input stream
	input := Range(4) // ValueStream containing [0 1 2 3]
	// 2) a stack of Transducers
	transducers := []Transducer{Map(Inc), Filter(Even)} // increment then filter odds
	// 3) a reducer to put at the bottom of the transducer stack
	reducer := Append() // very simple reducer - just appends values into a []interface{}
	// 4) a processor that puts it all together
	result := Transduce(input, reducer, transducers...)

	fmt.Println(result) // [2 4]


	// Or, we can use the Go processor, which does the work in a separate goroutine
	// and returns results through a channel.

	// Make an input chan, and stream each value from Range(4) into it
	in_chan := make(chan interface{}, 0)
	go StreamIntoChan(Range(4), in_chan)

	// Go provides its own bottom reducer (that's where it sends values out through
	// the return channel). So we don't provide one - just the input channel.
	out_chan := Go(in_chan, 0, transducers...)
    // Note that we reuse the transducer stack declared for the first example.
    // THIS. THIS is why transducers are cool.

	result2 := make([]interface{}, 0) // zero out the slice
	for v := range out_chan {
		result2 = append(result2, v)
	}

	fmt.Println(result) // [2 4]

}
```

Remember - what's important here is *not* the particular problem being solved, or the idiosyncracies of the Transduce or Go processors (you can always write your own). What's important is that we can reuse the Transducer stack we declared, and it works - regardless of eagerness vs laziness, parallelism, etc. That's what breaking down transformations into their smallest constituent parts gets us.

I also worked up another more sorta-real example in response to an idea on the mailing list: using transducers to [decode signals from airplane transponders](https://gist.github.com/sdboyer/4b116fd78d8bad07a9ff).

## The Arguments

I figure there's pros and cons to something like this. Makes sense to put em up front.

Please feel free to send PRs with more things to put in this section :)

### Cons

* Dodges around the type system - there is little to no compile-time safety here.
* To that end: is Yet Another Generics Attempt™...though, see [#1](https://github.com/sdboyer/transducers-go/issues/1).
* Syntax is not as fluid as Clojure's (though creating such things is kind of a Lisp specialty).
* Pursuant to all of the above, it'd be hard to call this idiomatic Go.
* The `ValueStream` notion is a bedrock for this system, and has significant flaws.
* Since this is based on streams/sequences/iteration, there will be cases where it is unequivocally less efficient than batch processing (slices).
* Performance in general. While Reflect is not used at all (duh), I haven't done perf analysis yet, so I'm not sure how much overhead we're looking at. The stream operations in particular (splitting, slice->stream->slice) probably mean a lot of heap allocs and duplication of data.
* re: performance - Go's compiler evidently not that great at inlining yet, and that's rather important for collapsing down the big function stack created by functional styles.

### Pros

* Stream-based data processing - so, amenable to dealing with continuous/infinite/larger-than-memory datasets.
* Sure, channels let you be stream-based. But they're [low-level primitives](https://gist.github.com/kachayev/21e7fe149bc5ae0bd878). Plus they're largely orthogonal to this, which is about decomposing processing pipelines into their constituent parts.
* Transducers could be an interesting, powerful way of structuring applications into segments, or for encapsulating library logic in a way that is easy to reuse, and whose purpose is widely understood.
* While the loss of type assurances hurts - a lot - the spec for transducer behavior is clear enough that it's probably feasible to aim at "correctness" via exhaustive black-box tests. (hah)
* And about types - I found a little kernel of something useful when looking beyond parametric polymorphism - [more here](https://github.com/sdboyer/transducers-go/issues/1).

## Glossary

Transducers have some jargon. Here's an attempt to cut it down. These go more or less in order.

* **Reduce:** If you're not familiar with the general concept of reduction, [LMGTFY](http://en.wikipedia.org/wiki/Fold_(higher-order_function)).
* **Reduce Step:** A [function](http://godoc.org/github.com/sdboyer/transducers-go#ReduceStep)/method with a reduce-like signature: `(accum, value) return`
* **Reducer:** A [set of three](http://godoc.org/github.com/sdboyer/transducers-go#Reducer) functions - the Reduce Step, plus Complete and Init methods.
* **Transducer:** A function that *transforms* a *reducing* function. They [take a Reducer and return a Reducer](http://godoc.org/github.com/sdboyer/transducers-go#Transducer).
* **Predicate:** Some transducers - for example, [Map](http://godoc.org/github.com/sdboyer/transducers-go#Map) and [Filter](http://godoc.org/github.com/sdboyer/transducers-go#Filter) - take a function to do their work. These injected functions are referred to as predicates.
* **Transducer stack:** In short: `[]Transducer`. A stack is stateless (it's just logic) and can be reused in as many processes as desired.
* **Bottom reducer:** The reducer that a stack of transducers will operate on.
* **Processor:** Processors take (at minimum) some kind of collection and a transducer stack, compose a transducer pipeline from the stack, and apply it across the elements of the collection.


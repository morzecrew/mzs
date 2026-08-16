package mzs

// Lazy sequences (§12.14).
//
// Everything else in the library materialises: `range` builds its elements, `map` builds
// a second array beside the first, and `MaxCollection` caps all of it at a million. That
// is the right default for a dialogue condition and the wrong one for a log — and after
// the `io` module (§12.13) a log is exactly what arrives. A `seq` is the answer: a source
// that is pulled one element at a time, with `map`/`filter`/`take` describing the work
// rather than doing it, and nothing built until a **terminal** row asks for a value.
//
//	(1..1_000_000_000).seq.filter { it % 7 == 0 }.take(3).array   # [7, 14, 21]
//
// Three things make this a kind of its own rather than a clever array:
//
//   - **A seq is a recipe, not a cursor.** Every terminal opens the source again, so
//     `s.len` and then `s.array` both see the whole sequence. Where the source has state
//     of its own — a generator closure over a counter, a reader that has already given
//     its bytes away — a second run sees what that state left, and that is what state
//     means. Nothing here caches: caching a sequence is `.array`, spelled out.
//   - **The budget ticks inside the chain.** Every source charges one step per element,
//     so the deadline and the step budget of §14.1 reach into a lazy pipeline exactly as
//     they reach into a `while`. An endless `seq` is a `while true` with better manners,
//     never a way around the limits.
//   - **It is not an array and does not pretend to be.** `type(s)` is `"seq"` and
//     `s.is("array")` is **false** — unlike a Range, which answers true (§12.10) because
//     it can be materialised on demand under a cap and a seq, by construction, cannot.
//     Host code that accepts one must not silently accept the other.
//
// The rows below are in two groups and the difference is the whole feature: a **lazy**
// row returns another seq and evaluates nothing; a **terminal** row pulls. Everything an
// array can do that needs the whole sequence at once — `sort`, `reverse`, `uniq`,
// `tally`, `group_by` — is reached by materialising first (`s.array.sort`), and that is
// deliberate: a row that quietly buffered a gigabyte would defeat the point of asking for
// a seq at all.

func init() {
	RegisterMethods(KSeq,
		// Lazy: each returns a new seq over this one and pulls nothing.
		Method{Name: "map", Min: 1, Max: 1, Fn: seqvMap},
		Method{Name: "filter", Min: 1, Max: 1, Fn: seqvFilter},
		Method{Name: "reject", Min: 1, Max: 1, Fn: seqvReject},
		Method{Name: "flat_map", Min: 1, Max: 1, Fn: seqvFlatMap},
		Method{Name: "take", Min: 1, Max: 1, Fn: seqvTake},
		Method{Name: "take_while", Min: 1, Max: 1, Fn: seqvTakeWhile},
		Method{Name: "drop", Min: 1, Max: 1, Fn: seqvDrop},
		Method{Name: "drop_while", Min: 1, Max: 1, Fn: seqvDropWhile},

		// Terminal: each pulls the source until it has its answer.
		Method{Name: "each", Min: 1, Max: 1, Fn: seqvEach},
		Method{Name: "each_with_index", Min: 1, Max: 1, Fn: seqvEachWithIndex},
		Method{Name: "reduce", Min: 1, Max: 2, Fn: seqvReduce},
		Method{Name: "array", Fn: seqvArray},
		Method{Name: "len", Fn: seqvLen},
		Method{Name: "empty", Fn: seqvEmpty},
		Method{Name: "count", Max: 1, Fn: seqvCount},
		Method{Name: "has", Min: 1, Max: 1, Fn: seqvHas},
		Method{Name: "first", Max: 1, Fn: seqvFirst},
		Method{Name: "find", Min: 1, Max: 1, Fn: seqvFind},
		Method{Name: "any", Max: 1, Fn: seqvAny},
		Method{Name: "all", Max: 1, Fn: seqvAll},
		Method{Name: "none", Max: 1, Fn: seqvNone},
		Method{Name: "sum", Max: 1, Fn: seqvSum},
		Method{Name: "min", Max: 1, Fn: seqvMin},
		Method{Name: "max", Max: 1, Fn: seqvMax},
		Method{Name: "join", Max: 1, Fn: seqvJoin},
	)

	RegisterBuiltin(Builtin{Name: "seq", Min: 1, Max: 1, Fn: func(c *Ctx, args []Value) (Value, error) {
		return toSeq(c, args[0])
	}})
}

// ---------------------------------------------------------------------------
// The value
// ---------------------------------------------------------------------------

// seqIter is one cursor over one sequence. It answers with the next element, or with
// ok == false when there is no next element, or with the error that ended the pull —
// a raise from a closure, a `break` on its way out (§8.10), or a limit (§14.1).
type seqIter func(c *Ctx) (Value, bool, error)

// Seq is the payload of a KSeq value: a way of starting the sequence over, and nothing
// else. It holds no elements and no position, which is what makes a chain of lazy rows a
// description that costs nothing until something pulls it.
type Seq struct {
	open func() seqIter
}

// seqSource builds a sequence from a cursor factory and is the only place a source is
// made. The step charge lives here rather than in the terminals, because the loop that
// can run away is *inside* a lazy row — a `filter` whose predicate never fires pulls
// forever without ever returning to the terminal that started it (§14.1).
func seqSource(open func() func(c *Ctx) (Value, bool, error)) *Seq {
	return &Seq{open: func() seqIter {
		next := open()
		return func(c *Ctx) (Value, bool, error) {
			if err := c.Step(1); err != nil {
				return Nil(), false, err
			}
			return next(c)
		}
	}}
}

// seqStage layers one lazy row over a sequence. wrap runs once per traversal, so the
// state a row needs — how many are left to take, whether the `drop_while` is over — is
// per-cursor and a second run of the same chain starts with it fresh.
func seqStage(s *Seq, wrap func(seqIter) seqIter) *Seq {
	return &Seq{open: func() seqIter { return wrap(s.open()) }}
}

// toSeq is the `seq` conversion (§12.1): an array or a range becomes a lazy view of
// itself, a function becomes a generator, and a seq is already one. Nothing else
// converts — a scalar has no elements to hand out one at a time.
func toSeq(c *Ctx, v Value) (Value, error) {
	switch v.Kind() {
	case KSeq:
		return v, nil
	case KArray:
		return seqOf(seqOfArray(v)), nil
	case KRange:
		return seqOf(seqOfRange(v.rng())), nil
	case KFunc:
		return seqOf(seqOfFunc(v)), nil
	}
	return Nil(), c.TypeErrorf("%s expects an array, a range, a function or a seq, got %s", c.Name(), v.TypeName())
}

// seqOfArray reads the array's backing slice at the start of each traversal, which is the
// rule `each` already follows: an element pushed while the loop runs does not extend it.
func seqOfArray(v Value) *Seq {
	return seqSource(func() func(*Ctx) (Value, bool, error) {
		xs := *v.arr()
		i := 0
		return func(*Ctx) (Value, bool, error) {
			if i >= len(xs) {
				return Nil(), false, nil
			}
			x := xs[i]
			i++
			return x, true, nil
		}
	})
}

// seqOfRange counts rather than materialises, so `(1..1e18).seq` is a value like any
// other while `(1..1e18).array` is the limit error it should be (§14.2). The last element
// is handed out before the counter is advanced, because a range may end at the largest
// int there is and `i++` past it would wrap.
func seqOfRange(r *Range) *Seq {
	last := r.Hi
	if r.Excl {
		last--
	}
	return seqSource(func() func(*Ctx) (Value, bool, error) {
		i, done := r.Lo, r.Lo > last
		return func(*Ctx) (Value, bool, error) {
			if done {
				return Nil(), false, nil
			}
			v := Int(i)
			if i == last {
				done = true
			}
			i++
			return v, true, nil
		}
	})
}

// seqOfFunc is the generator: the closure is called with the index of the element it is
// being asked for, and **nil ends the sequence**. That is the whole protocol — there is
// no `yield` (§20) and no coroutine anywhere in this package — and it is why a seq built
// this way cannot contain nil. A generator that ignores its index and reads a counter of
// its own is a stateful source, and a second traversal continues where the first stopped.
func seqOfFunc(fn Value) *Seq {
	return seqSource(func() func(*Ctx) (Value, bool, error) {
		i := int64(0)
		done := false
		return func(c *Ctx) (Value, bool, error) {
			if done {
				return Nil(), false, nil
			}
			v, err := c.Call(fn, Int(i))
			if err != nil {
				return Nil(), false, err
			}
			i++
			if v.IsNil() {
				done = true
				return Nil(), false, nil
			}
			return v, true, nil
		}
	})
}

// ---------------------------------------------------------------------------
// Row plumbing
// ---------------------------------------------------------------------------

// seqRecv is the receiver check every row starts with.
func seqRecv(c *Ctx, v Value) (*Seq, error) {
	if s := v.seq(); s != nil {
		return s, nil
	}
	return nil, c.TypeErrorf("%s expects a seq, got %s", c.Name(), v.TypeName())
}

// seqFn reads the closure a row was given. It is the trailing closure of §4.2, which is
// simply the last argument of kind function, so `s.map { … }` and `s.map(double)` are one
// call. A lazy row checks it **here**, when the stage is built, rather than at the first
// pull: `s.map(3)` must say so where it is written, even if nothing ever pulls the seq.
func seqFn(c *Ctx, args []Value) (Value, error) {
	if n := len(args); n > 0 && args[n-1].Kind() == KFunc {
		return args[n-1], nil
	}
	return Nil(), c.TypeErrorf("%s expects a closure, got %s", c.Name(), argAt(args, 0).TypeName())
}

// seqRun pulls a sequence from its start and hands each element to f, stopping when f
// says so, when the sequence ends, or when anything fails. Every terminal row is this
// loop and a little bookkeeping.
func seqRun(c *Ctx, s *Seq, f func(Value) (bool, error)) error {
	it := s.open()
	for {
		v, ok, err := it(c)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		more, err := f(v)
		if err != nil {
			return err
		}
		if !more {
			return nil
		}
	}
}

// seqLazy is the shape every lazy row shares: check the receiver, check the closure,
// return a new seq over the old one.
func seqLazy(c *Ctx, recv Value, args []Value, wrap func(s *Seq, fn Value) *Seq) (Value, error) {
	s, err := seqRecv(c, recv)
	if err != nil {
		return Nil(), err
	}
	fn, err := seqFn(c, args)
	if err != nil {
		return Nil(), err
	}
	return seqOf(wrap(s, fn)), nil
}

// seqTerm is the shape every terminal row shares.
func seqTerm(c *Ctx, recv Value, f func(s *Seq) (Value, error)) (Value, error) {
	s, err := seqRecv(c, recv)
	if err != nil {
		return Nil(), err
	}
	return f(s)
}

// ---------------------------------------------------------------------------
// Lazy rows
// ---------------------------------------------------------------------------

func seqvMap(c *Ctx, recv Value, args []Value) (Value, error) {
	return seqLazy(c, recv, args, func(s *Seq, fn Value) *Seq {
		return seqStage(s, func(it seqIter) seqIter {
			return func(c *Ctx) (Value, bool, error) {
				v, ok, err := it(c)
				if err != nil || !ok {
					return Nil(), false, err
				}
				out, err := c.Call(fn, v)
				if err != nil {
					return Nil(), false, err
				}
				return out, true, nil
			}
		})
	})
}

func seqvFilter(c *Ctx, recv Value, args []Value) (Value, error) {
	return seqLazy(c, recv, args, func(s *Seq, fn Value) *Seq { return seqSieve(s, fn, true) })
}

func seqvReject(c *Ctx, recv Value, args []Value) (Value, error) {
	return seqLazy(c, recv, args, func(s *Seq, fn Value) *Seq { return seqSieve(s, fn, false) })
}

// seqSieve is filter and reject: the loop that keeps pulling until one element answers
// the way the row wants. It is the loop the step charge of seqSource exists for — a
// predicate that is never true over an endless source ends on the budget, not on nothing.
func seqSieve(s *Seq, fn Value, keep bool) *Seq {
	return seqStage(s, func(it seqIter) seqIter {
		return func(c *Ctx) (Value, bool, error) {
			for {
				v, ok, err := it(c)
				if err != nil || !ok {
					return Nil(), false, err
				}
				r, err := c.Call(fn, v)
				if err != nil {
					return Nil(), false, err
				}
				if r.Truthy() == keep {
					return v, true, nil
				}
			}
		}
	})
}

// seqvFlatMap flattens one level, as the array row does (§12.3), and does it lazily: an
// array, a range or a seq the closure returns is pulled element by element, and anything
// else is one element. A range is *not* materialised on the way through, so
// `s.flat_map { (1..it) }` costs nothing per element it does not reach.
func seqvFlatMap(c *Ctx, recv Value, args []Value) (Value, error) {
	return seqLazy(c, recv, args, func(s *Seq, fn Value) *Seq {
		return seqStage(s, func(it seqIter) seqIter {
			var inner seqIter
			return func(c *Ctx) (Value, bool, error) {
				for {
					if inner != nil {
						v, ok, err := inner(c)
						if err != nil {
							return Nil(), false, err
						}
						if ok {
							return v, true, nil
						}
						inner = nil
					}
					v, ok, err := it(c)
					if err != nil || !ok {
						return Nil(), false, err
					}
					out, err := c.Call(fn, v)
					if err != nil {
						return Nil(), false, err
					}
					switch out.Kind() {
					case KArray, KRange, KSeq:
						sv, err := toSeq(c, out)
						if err != nil {
							return Nil(), false, err
						}
						inner = sv.seq().open()
					default:
						return out, true, nil
					}
				}
			}
		})
	})
}

func seqvTake(c *Ctx, recv Value, args []Value) (Value, error) {
	s, err := seqRecv(c, recv)
	if err != nil {
		return Nil(), err
	}
	n, err := arrCountArg(c, args[0])
	if err != nil {
		return Nil(), err
	}
	return seqOf(seqStage(s, func(it seqIter) seqIter {
		left := n
		return func(c *Ctx) (Value, bool, error) {
			if left <= 0 {
				return Nil(), false, nil
			}
			v, ok, err := it(c)
			if err != nil || !ok {
				return Nil(), false, err
			}
			left--
			return v, true, nil
		}
	})), nil
}

func seqvDrop(c *Ctx, recv Value, args []Value) (Value, error) {
	s, err := seqRecv(c, recv)
	if err != nil {
		return Nil(), err
	}
	n, err := arrCountArg(c, args[0])
	if err != nil {
		return Nil(), err
	}
	return seqOf(seqStage(s, func(it seqIter) seqIter {
		left := n
		return func(c *Ctx) (Value, bool, error) {
			for left > 0 {
				_, ok, err := it(c)
				if err != nil || !ok {
					return Nil(), false, err
				}
				left--
			}
			return it(c)
		}
	})), nil
}

// seqvTakeWhile ends the sequence at the first element the closure refuses, and stays
// ended: the element that failed is not pulled twice and nothing after it is pulled at
// all, which is what makes `s.take_while { it < 100 }` finite over an endless source.
func seqvTakeWhile(c *Ctx, recv Value, args []Value) (Value, error) {
	return seqLazy(c, recv, args, func(s *Seq, fn Value) *Seq {
		return seqStage(s, func(it seqIter) seqIter {
			done := false
			return func(c *Ctx) (Value, bool, error) {
				if done {
					return Nil(), false, nil
				}
				v, ok, err := it(c)
				if err != nil || !ok {
					return Nil(), false, err
				}
				r, err := c.Call(fn, v)
				if err != nil {
					return Nil(), false, err
				}
				if !r.Truthy() {
					done = true
					return Nil(), false, nil
				}
				return v, true, nil
			}
		})
	})
}

func seqvDropWhile(c *Ctx, recv Value, args []Value) (Value, error) {
	return seqLazy(c, recv, args, func(s *Seq, fn Value) *Seq {
		return seqStage(s, func(it seqIter) seqIter {
			dropping := true
			return func(c *Ctx) (Value, bool, error) {
				for {
					v, ok, err := it(c)
					if err != nil || !ok {
						return Nil(), false, err
					}
					if !dropping {
						return v, true, nil
					}
					r, err := c.Call(fn, v)
					if err != nil {
						return Nil(), false, err
					}
					if !r.Truthy() {
						dropping = false
						return v, true, nil
					}
				}
			}
		})
	})
}

// ---------------------------------------------------------------------------
// Terminal rows
// ---------------------------------------------------------------------------

// seqvEach returns the receiver, as the array row does, so a chain may go on being
// described after it has been walked once.
func seqvEach(c *Ctx, recv Value, args []Value) (Value, error) {
	if _, err := seqTerm(c, recv, func(s *Seq) (Value, error) {
		return Nil(), seqRun(c, s, func(v Value) (bool, error) {
			_, err := c.CallClosure(v)
			return true, err
		})
	}); err != nil {
		return Nil(), err
	}
	return recv, nil
}

func seqvEachWithIndex(c *Ctx, recv Value, args []Value) (Value, error) {
	if _, err := seqTerm(c, recv, func(s *Seq) (Value, error) {
		i := int64(0)
		return Nil(), seqRun(c, s, func(v Value) (bool, error) {
			_, err := c.CallClosure(v, Int(i))
			i++
			return true, err
		})
	}); err != nil {
		return Nil(), err
	}
	return recv, nil
}

// seqvArray is the materialisation, and the one row that charges MaxCollection: this is
// where a lazy pipeline becomes memory (§14.2).
func seqvArray(c *Ctx, recv Value, args []Value) (Value, error) {
	return seqTerm(c, recv, func(s *Seq) (Value, error) {
		out := []Value{}
		err := seqRun(c, s, func(v Value) (bool, error) {
			out = append(out, v)
			return true, c.CheckCollection(len(out))
		})
		if err != nil {
			return Nil(), err
		}
		return arrayOf(out), nil
	})
}

func seqvLen(c *Ctx, recv Value, args []Value) (Value, error) {
	return seqTerm(c, recv, func(s *Seq) (Value, error) {
		n := int64(0)
		err := seqRun(c, s, func(Value) (bool, error) {
			n++
			return true, nil
		})
		return Int(n), err
	})
}

// seqvEmpty pulls exactly one element and no more.
func seqvEmpty(c *Ctx, recv Value, args []Value) (Value, error) {
	return seqTerm(c, recv, func(s *Seq) (Value, error) {
		any := false
		err := seqRun(c, s, func(Value) (bool, error) {
			any = true
			return false, nil
		})
		return Bool(!any), err
	})
}

// seqvCount counts every element, the ones equal to a value, or the ones a closure
// accepts — the three forms of the array row (§12.3).
func seqvCount(c *Ctx, recv Value, args []Value) (Value, error) {
	return seqTerm(c, recv, func(s *Seq) (Value, error) {
		pos := arrArgs(c, args)
		n := int64(0)
		err := seqRun(c, s, func(v Value) (bool, error) {
			switch {
			case len(pos) == 1:
				if v.Equal(pos[0]) {
					n++
				}
			case c.HasClosure():
				r, err := c.CallClosure(v)
				if err != nil {
					return false, err
				}
				if r.Truthy() {
					n++
				}
			default:
				n++
			}
			return true, nil
		})
		return Int(n), err
	})
}

// seqvHas is membership, and therefore what `x in s` asks (§8.5). It stops at the first
// match, so it is finite over an endless source whenever the element is there.
func seqvHas(c *Ctx, recv Value, args []Value) (Value, error) {
	return seqTerm(c, recv, func(s *Seq) (Value, error) {
		found := false
		err := seqRun(c, s, func(v Value) (bool, error) {
			found = v.Equal(args[0])
			return !found, nil
		})
		return Bool(found), err
	})
}

// seqvFirst is the first element, or the first n as an array (§12.3). Without an argument
// an empty sequence is nil, which is what `s.first ?? fallback` wants.
func seqvFirst(c *Ctx, recv Value, args []Value) (Value, error) {
	if len(args) == 0 {
		return seqTerm(c, recv, func(s *Seq) (Value, error) {
			out := Nil()
			err := seqRun(c, s, func(v Value) (bool, error) {
				out = v
				return false, nil
			})
			return out, err
		})
	}
	n, err := arrCountArg(c, args[0])
	if err != nil {
		return Nil(), err
	}
	return seqTerm(c, recv, func(s *Seq) (Value, error) {
		if err := c.CheckCollection(n); err != nil {
			return Nil(), err
		}
		out := make([]Value, 0, min(n, 64))
		err := seqRun(c, s, func(v Value) (bool, error) {
			if len(out) >= n {
				return false, nil
			}
			out = append(out, v)
			return len(out) < n, nil
		})
		if err != nil {
			return Nil(), err
		}
		return arrayOf(out), nil
	})
}

func seqvFind(c *Ctx, recv Value, args []Value) (Value, error) {
	return seqTerm(c, recv, func(s *Seq) (Value, error) {
		out := Nil()
		err := seqRun(c, s, func(v Value) (bool, error) {
			r, err := c.CallClosure(v)
			if err != nil {
				return false, err
			}
			if r.Truthy() {
				out = v
				return false, nil
			}
			return true, nil
		})
		return out, err
	})
}

func seqvAny(c *Ctx, recv Value, args []Value) (Value, error) {
	return seqQuantify(c, recv, args, "any")
}

func seqvAll(c *Ctx, recv Value, args []Value) (Value, error) {
	return seqQuantify(c, recv, args, "all")
}

func seqvNone(c *Ctx, recv Value, args []Value) (Value, error) {
	return seqQuantify(c, recv, args, "none")
}

// seqQuantify is any/all/none, short-circuiting on the first element that decides the
// answer. Without a closure the elements are tested for truthiness (D6), as on an array.
func seqQuantify(c *Ctx, recv Value, args []Value, mode string) (Value, error) {
	if err := arrOnlyClosure(c, args); err != nil {
		return Nil(), err
	}
	return seqTerm(c, recv, func(s *Seq) (Value, error) {
		out := mode != "any"
		err := seqRun(c, s, func(v Value) (bool, error) {
			hit := v.Truthy()
			if c.HasClosure() {
				r, err := c.CallClosure(v)
				if err != nil {
					return false, err
				}
				hit = r.Truthy()
			}
			switch mode {
			case "any":
				if hit {
					out = true
					return false, nil
				}
			case "all":
				if !hit {
					out = false
					return false, nil
				}
			case "none":
				if hit {
					out = false
					return false, nil
				}
			}
			return true, nil
		})
		return Bool(out), err
	})
}

// seqvReduce seeds with the first element when no initial value is given, as the array
// row does.
func seqvReduce(c *Ctx, recv Value, args []Value) (Value, error) {
	return seqTerm(c, recv, func(s *Seq) (Value, error) {
		pos := arrArgs(c, args)
		acc := Nil()
		seeded := len(pos) == 1
		if seeded {
			acc = pos[0]
		}
		err := seqRun(c, s, func(v Value) (bool, error) {
			if !seeded {
				acc, seeded = v, true
				return true, nil
			}
			r, err := c.CallClosure(acc, v)
			if err != nil {
				return false, err
			}
			acc = r
			return true, nil
		})
		return acc, err
	})
}

func seqvSum(c *Ctx, recv Value, args []Value) (Value, error) {
	if err := arrOnlyClosure(c, args); err != nil {
		return Nil(), err
	}
	return seqTerm(c, recv, func(s *Seq) (Value, error) {
		acc := Int(0)
		err := seqRun(c, s, func(v Value) (bool, error) {
			x := v
			if c.HasClosure() {
				r, err := c.CallClosure(v)
				if err != nil {
					return false, err
				}
				x = r
			}
			sum, err := arrAdd(c, acc, x)
			if err != nil {
				return false, err
			}
			acc = sum
			return true, nil
		})
		return acc, err
	})
}

func seqvMin(c *Ctx, recv Value, args []Value) (Value, error) {
	return seqExtreme(c, recv, args, -1)
}

func seqvMax(c *Ctx, recv Value, args []Value) (Value, error) {
	return seqExtreme(c, recv, args, 1)
}

// seqExtreme takes the optional comparator closure the array rows take, and holds one
// element rather than the sequence.
func seqExtreme(c *Ctx, recv Value, args []Value, want int) (Value, error) {
	if err := arrOnlyClosure(c, args); err != nil {
		return Nil(), err
	}
	return seqTerm(c, recv, func(s *Seq) (Value, error) {
		best, seen := Nil(), false
		err := seqRun(c, s, func(v Value) (bool, error) {
			if !seen {
				best, seen = v, true
				return true, nil
			}
			var n int
			if c.HasClosure() {
				r, err := c.CallClosure(v, best)
				if err != nil {
					return false, err
				}
				n = int(r.Int())
			} else {
				cmp, err := arrCompare(c, v, best)
				if err != nil {
					return false, err
				}
				n = cmp
			}
			if (want < 0 && n < 0) || (want > 0 && n > 0) {
				best = v
			}
			return true, nil
		})
		return best, err
	})
}

// seqvJoin renders each element with `str` and charges the string cap as it grows, so a
// join over an endless sequence ends on MaxStringBytes rather than on memory (§14.2).
func seqvJoin(c *Ctx, recv Value, args []Value) (Value, error) {
	sep := ""
	if pos := arrArgs(c, args); len(pos) > 0 {
		s, err := argStr(c, pos[0])
		if err != nil {
			return Nil(), err
		}
		sep = s
	}
	return seqTerm(c, recv, func(s *Seq) (Value, error) {
		var sb []byte
		first := true
		err := seqRun(c, s, func(v Value) (bool, error) {
			if !first {
				sb = append(sb, sep...)
			}
			first = false
			sb = append(sb, v.Str()...)
			return true, c.CheckString(len(sb))
		})
		if err != nil {
			return Nil(), err
		}
		return Str(string(sb)), nil
	})
}

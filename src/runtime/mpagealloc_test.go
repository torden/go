// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"fmt"
	. "runtime"
	"testing"
)

func checkPageAlloc(t *testing.T, want, got *PageAlloc) {
	// Ensure start and end are correct.
	wantStart, wantEnd := want.Bounds()
	gotStart, gotEnd := got.Bounds()
	if gotStart != wantStart {
		t.Fatalf("start values not equal: got %d, want %d", gotStart, wantStart)
	}
	if gotEnd != wantEnd {
		t.Fatalf("end values not equal: got %d, want %d", gotEnd, wantEnd)
	}

	for i := gotStart; i < gotEnd; i++ {
		// Check the bitmaps. Note that we may have nil data.
		gb, wb := got.PallocData(i), want.PallocData(i)
		if gb == nil && wb == nil {
			continue
		}
		if (gb == nil && wb != nil) || (gb != nil && wb == nil) {
			t.Errorf("chunk %d nilness mismatch", i)
		}
		if !checkPallocBits(t, gb.PallocBits(), wb.PallocBits()) {
			t.Logf("in chunk %d (mallocBits)", i)
		}
		if !checkPallocBits(t, gb.Scavenged(), wb.Scavenged()) {
			t.Logf("in chunk %d (scavenged)", i)
		}
	}
	// TODO(mknyszek): Verify summaries too?
}

func TestPageAllocGrow(t *testing.T) {
	type test struct {
		chunks []ChunkIdx
		inUse  []AddrRange
	}
	tests := map[string]test{
		"One": {
			chunks: []ChunkIdx{
				BaseChunkIdx,
			},
			inUse: []AddrRange{
				{PageBase(BaseChunkIdx, 0), PageBase(BaseChunkIdx+1, 0)},
			},
		},
		"Contiguous2": {
			chunks: []ChunkIdx{
				BaseChunkIdx,
				BaseChunkIdx + 1,
			},
			inUse: []AddrRange{
				{PageBase(BaseChunkIdx, 0), PageBase(BaseChunkIdx+2, 0)},
			},
		},
		"Contiguous5": {
			chunks: []ChunkIdx{
				BaseChunkIdx,
				BaseChunkIdx + 1,
				BaseChunkIdx + 2,
				BaseChunkIdx + 3,
				BaseChunkIdx + 4,
			},
			inUse: []AddrRange{
				{PageBase(BaseChunkIdx, 0), PageBase(BaseChunkIdx+5, 0)},
			},
		},
		"Discontiguous": {
			chunks: []ChunkIdx{
				BaseChunkIdx,
				BaseChunkIdx + 2,
				BaseChunkIdx + 4,
			},
			inUse: []AddrRange{
				{PageBase(BaseChunkIdx, 0), PageBase(BaseChunkIdx+1, 0)},
				{PageBase(BaseChunkIdx+2, 0), PageBase(BaseChunkIdx+3, 0)},
				{PageBase(BaseChunkIdx+4, 0), PageBase(BaseChunkIdx+5, 0)},
			},
		},
		"Mixed": {
			chunks: []ChunkIdx{
				BaseChunkIdx,
				BaseChunkIdx + 1,
				BaseChunkIdx + 2,
				BaseChunkIdx + 4,
			},
			inUse: []AddrRange{
				{PageBase(BaseChunkIdx, 0), PageBase(BaseChunkIdx+3, 0)},
				{PageBase(BaseChunkIdx+4, 0), PageBase(BaseChunkIdx+5, 0)},
			},
		},
		"WildlyDiscontiguous": {
			chunks: []ChunkIdx{
				BaseChunkIdx,
				BaseChunkIdx + 1,
				BaseChunkIdx + 0x10,
				BaseChunkIdx + 0x21,
			},
			inUse: []AddrRange{
				{PageBase(BaseChunkIdx, 0), PageBase(BaseChunkIdx+2, 0)},
				{PageBase(BaseChunkIdx+0x10, 0), PageBase(BaseChunkIdx+0x11, 0)},
				{PageBase(BaseChunkIdx+0x21, 0), PageBase(BaseChunkIdx+0x22, 0)},
			},
		},
	}
	if PageAlloc64Bit != 0 {
		tests["ExtremelyDiscontiguous"] = test{
			chunks: []ChunkIdx{
				BaseChunkIdx,
				BaseChunkIdx + 0x100000, // constant translates to O(TiB)
			},
			inUse: []AddrRange{
				{PageBase(BaseChunkIdx, 0), PageBase(BaseChunkIdx+1, 0)},
				{PageBase(BaseChunkIdx+0x100000, 0), PageBase(BaseChunkIdx+0x100001, 0)},
			},
		}
	}
	for name, v := range tests {
		v := v
		t.Run(name, func(t *testing.T) {
			// By creating a new pageAlloc, we will
			// grow it for each chunk defined in x.
			x := make(map[ChunkIdx][]BitRange)
			for _, c := range v.chunks {
				x[c] = []BitRange{}
			}
			b := NewPageAlloc(x, nil)
			defer FreePageAlloc(b)

			got := b.InUse()
			want := v.inUse

			// Check for mismatches.
			if len(got) != len(want) {
				t.Fail()
			} else {
				for i := range want {
					if want[i] != got[i] {
						t.Fail()
						break
					}
				}
			}
			if t.Failed() {
				t.Logf("found inUse mismatch")
				t.Logf("got:")
				for i, r := range got {
					t.Logf("\t#%d [0x%x, 0x%x)", i, r.Base, r.Limit)
				}
				t.Logf("want:")
				for i, r := range want {
					t.Logf("\t#%d [0x%x, 0x%x)", i, r.Base, r.Limit)
				}
			}
		})
	}
}

func TestPageAllocAlloc(t *testing.T) {
	type hit struct {
		npages, base, scav uintptr
	}
	tests := map[string]struct {
		scav   map[ChunkIdx][]BitRange
		before map[ChunkIdx][]BitRange
		after  map[ChunkIdx][]BitRange
		hits   []hit
	}{
		"AllFree1": {
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {},
			},
			scav: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{0, 1}, {2, 2}},
			},
			hits: []hit{
				{1, PageBase(BaseChunkIdx, 0), PageSize},
				{1, PageBase(BaseChunkIdx, 1), 0},
				{1, PageBase(BaseChunkIdx, 2), PageSize},
				{1, PageBase(BaseChunkIdx, 3), PageSize},
				{1, PageBase(BaseChunkIdx, 4), 0},
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{0, 5}},
			},
		},
		"ManyArena1": {
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages}},
				BaseChunkIdx + 1: {{0, PallocChunkPages}},
				BaseChunkIdx + 2: {{0, PallocChunkPages - 1}},
			},
			scav: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages}},
				BaseChunkIdx + 1: {{0, PallocChunkPages}},
				BaseChunkIdx + 2: {{0, PallocChunkPages}},
			},
			hits: []hit{
				{1, PageBase(BaseChunkIdx+2, PallocChunkPages-1), PageSize},
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages}},
				BaseChunkIdx + 1: {{0, PallocChunkPages}},
				BaseChunkIdx + 2: {{0, PallocChunkPages}},
			},
		},
		"NotContiguous1": {
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx:        {{0, PallocChunkPages}},
				BaseChunkIdx + 0xff: {{0, 0}},
			},
			scav: map[ChunkIdx][]BitRange{
				BaseChunkIdx:        {{0, PallocChunkPages}},
				BaseChunkIdx + 0xff: {{0, PallocChunkPages}},
			},
			hits: []hit{
				{1, PageBase(BaseChunkIdx+0xff, 0), PageSize},
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx:        {{0, PallocChunkPages}},
				BaseChunkIdx + 0xff: {{0, 1}},
			},
		},
		"AllFree2": {
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {},
			},
			scav: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{0, 3}, {7, 1}},
			},
			hits: []hit{
				{2, PageBase(BaseChunkIdx, 0), 2 * PageSize},
				{2, PageBase(BaseChunkIdx, 2), PageSize},
				{2, PageBase(BaseChunkIdx, 4), 0},
				{2, PageBase(BaseChunkIdx, 6), PageSize},
				{2, PageBase(BaseChunkIdx, 8), 0},
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{0, 10}},
			},
		},
		"Straddle2": {
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages - 1}},
				BaseChunkIdx + 1: {{1, PallocChunkPages - 1}},
			},
			scav: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{PallocChunkPages - 1, 1}},
				BaseChunkIdx + 1: {},
			},
			hits: []hit{
				{2, PageBase(BaseChunkIdx, PallocChunkPages-1), PageSize},
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages}},
				BaseChunkIdx + 1: {{0, PallocChunkPages}},
			},
		},
		"AllFree5": {
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {},
			},
			scav: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{0, 8}, {9, 1}, {17, 5}},
			},
			hits: []hit{
				{5, PageBase(BaseChunkIdx, 0), 5 * PageSize},
				{5, PageBase(BaseChunkIdx, 5), 4 * PageSize},
				{5, PageBase(BaseChunkIdx, 10), 0},
				{5, PageBase(BaseChunkIdx, 15), 3 * PageSize},
				{5, PageBase(BaseChunkIdx, 20), 2 * PageSize},
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{0, 25}},
			},
		},
		"AllFree64": {
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {},
			},
			scav: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{21, 1}, {63, 65}},
			},
			hits: []hit{
				{64, PageBase(BaseChunkIdx, 0), 2 * PageSize},
				{64, PageBase(BaseChunkIdx, 64), 64 * PageSize},
				{64, PageBase(BaseChunkIdx, 128), 0},
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{0, 192}},
			},
		},
		"AllFree65": {
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {},
			},
			scav: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{129, 1}},
			},
			hits: []hit{
				{65, PageBase(BaseChunkIdx, 0), 0},
				{65, PageBase(BaseChunkIdx, 65), PageSize},
				{65, PageBase(BaseChunkIdx, 130), 0},
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{0, 195}},
			},
		},
		// TODO(mknyszek): Add tests close to the chunk size.
		"ExhaustPallocChunkPages-3": {
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {},
			},
			scav: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{10, 1}},
			},
			hits: []hit{
				{PallocChunkPages - 3, PageBase(BaseChunkIdx, 0), PageSize},
				{PallocChunkPages - 3, 0, 0},
				{1, PageBase(BaseChunkIdx, PallocChunkPages-3), 0},
				{2, PageBase(BaseChunkIdx, PallocChunkPages-2), 0},
				{1, 0, 0},
				{PallocChunkPages - 3, 0, 0},
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{0, PallocChunkPages}},
			},
		},
		"AllFreePallocChunkPages": {
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {},
			},
			scav: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{0, 1}, {PallocChunkPages - 1, 1}},
			},
			hits: []hit{
				{PallocChunkPages, PageBase(BaseChunkIdx, 0), 2 * PageSize},
				{PallocChunkPages, 0, 0},
				{1, 0, 0},
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{0, PallocChunkPages}},
			},
		},
		"StraddlePallocChunkPages": {
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages / 2}},
				BaseChunkIdx + 1: {{PallocChunkPages / 2, PallocChunkPages / 2}},
			},
			scav: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {},
				BaseChunkIdx + 1: {{3, 100}},
			},
			hits: []hit{
				{PallocChunkPages, PageBase(BaseChunkIdx, PallocChunkPages/2), 100 * PageSize},
				{PallocChunkPages, 0, 0},
				{1, 0, 0},
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages}},
				BaseChunkIdx + 1: {{0, PallocChunkPages}},
			},
		},
		"StraddlePallocChunkPages+1": {
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages / 2}},
				BaseChunkIdx + 1: {},
			},
			scav: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages}},
				BaseChunkIdx + 1: {{0, PallocChunkPages}},
			},
			hits: []hit{
				{PallocChunkPages + 1, PageBase(BaseChunkIdx, PallocChunkPages/2), (PallocChunkPages + 1) * PageSize},
				{PallocChunkPages, 0, 0},
				{1, PageBase(BaseChunkIdx+1, PallocChunkPages/2+1), PageSize},
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages}},
				BaseChunkIdx + 1: {{0, PallocChunkPages/2 + 2}},
			},
		},
		"AllFreePallocChunkPages*2": {
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {},
				BaseChunkIdx + 1: {},
			},
			scav: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {},
				BaseChunkIdx + 1: {},
			},
			hits: []hit{
				{PallocChunkPages * 2, PageBase(BaseChunkIdx, 0), 0},
				{PallocChunkPages * 2, 0, 0},
				{1, 0, 0},
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages}},
				BaseChunkIdx + 1: {{0, PallocChunkPages}},
			},
		},
		"NotContiguousPallocChunkPages*2": {
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx:        {},
				BaseChunkIdx + 0x40: {},
				BaseChunkIdx + 0x41: {},
			},
			scav: map[ChunkIdx][]BitRange{
				BaseChunkIdx:        {{0, PallocChunkPages}},
				BaseChunkIdx + 0x40: {},
				BaseChunkIdx + 0x41: {},
			},
			hits: []hit{
				{PallocChunkPages * 2, PageBase(BaseChunkIdx+0x40, 0), 0},
				{21, PageBase(BaseChunkIdx, 0), 21 * PageSize},
				{1, PageBase(BaseChunkIdx, 21), PageSize},
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx:        {{0, 22}},
				BaseChunkIdx + 0x40: {{0, PallocChunkPages}},
				BaseChunkIdx + 0x41: {{0, PallocChunkPages}},
			},
		},
		"StraddlePallocChunkPages*2": {
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages / 2}},
				BaseChunkIdx + 1: {},
				BaseChunkIdx + 2: {{PallocChunkPages / 2, PallocChunkPages / 2}},
			},
			scav: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, 7}},
				BaseChunkIdx + 1: {{3, 5}, {121, 10}},
				BaseChunkIdx + 2: {{PallocChunkPages/2 + 12, 2}},
			},
			hits: []hit{
				{PallocChunkPages * 2, PageBase(BaseChunkIdx, PallocChunkPages/2), 15 * PageSize},
				{PallocChunkPages * 2, 0, 0},
				{1, 0, 0},
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages}},
				BaseChunkIdx + 1: {{0, PallocChunkPages}},
				BaseChunkIdx + 2: {{0, PallocChunkPages}},
			},
		},
		"StraddlePallocChunkPages*5/4": {
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages}},
				BaseChunkIdx + 1: {{0, PallocChunkPages * 3 / 4}},
				BaseChunkIdx + 2: {{0, PallocChunkPages * 3 / 4}},
				BaseChunkIdx + 3: {{0, 0}},
			},
			scav: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages}},
				BaseChunkIdx + 1: {{PallocChunkPages / 2, PallocChunkPages/4 + 1}},
				BaseChunkIdx + 2: {{PallocChunkPages / 3, 1}},
				BaseChunkIdx + 3: {{PallocChunkPages * 2 / 3, 1}},
			},
			hits: []hit{
				{PallocChunkPages * 5 / 4, PageBase(BaseChunkIdx+2, PallocChunkPages*3/4), PageSize},
				{PallocChunkPages * 5 / 4, 0, 0},
				{1, PageBase(BaseChunkIdx+1, PallocChunkPages*3/4), PageSize},
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages}},
				BaseChunkIdx + 1: {{0, PallocChunkPages*3/4 + 1}},
				BaseChunkIdx + 2: {{0, PallocChunkPages}},
				BaseChunkIdx + 3: {{0, PallocChunkPages}},
			},
		},
		"AllFreePallocChunkPages*7+5": {
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {},
				BaseChunkIdx + 1: {},
				BaseChunkIdx + 2: {},
				BaseChunkIdx + 3: {},
				BaseChunkIdx + 4: {},
				BaseChunkIdx + 5: {},
				BaseChunkIdx + 6: {},
				BaseChunkIdx + 7: {},
			},
			scav: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{50, 1}},
				BaseChunkIdx + 1: {{31, 1}},
				BaseChunkIdx + 2: {{7, 1}},
				BaseChunkIdx + 3: {{200, 1}},
				BaseChunkIdx + 4: {{3, 1}},
				BaseChunkIdx + 5: {{51, 1}},
				BaseChunkIdx + 6: {{20, 1}},
				BaseChunkIdx + 7: {{1, 1}},
			},
			hits: []hit{
				{PallocChunkPages*7 + 5, PageBase(BaseChunkIdx, 0), 8 * PageSize},
				{PallocChunkPages*7 + 5, 0, 0},
				{1, PageBase(BaseChunkIdx+7, 5), 0},
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages}},
				BaseChunkIdx + 1: {{0, PallocChunkPages}},
				BaseChunkIdx + 2: {{0, PallocChunkPages}},
				BaseChunkIdx + 3: {{0, PallocChunkPages}},
				BaseChunkIdx + 4: {{0, PallocChunkPages}},
				BaseChunkIdx + 5: {{0, PallocChunkPages}},
				BaseChunkIdx + 6: {{0, PallocChunkPages}},
				BaseChunkIdx + 7: {{0, 6}},
			},
		},
	}
	for name, v := range tests {
		v := v
		t.Run(name, func(t *testing.T) {
			b := NewPageAlloc(v.before, v.scav)
			defer FreePageAlloc(b)

			for iter, i := range v.hits {
				a, s := b.Alloc(i.npages)
				if a != i.base {
					t.Fatalf("bad alloc #%d: want base 0x%x, got 0x%x", iter+1, i.base, a)
				}
				if s != i.scav {
					t.Fatalf("bad alloc #%d: want scav %d, got %d", iter+1, i.scav, s)
				}
			}
			want := NewPageAlloc(v.after, v.scav)
			defer FreePageAlloc(want)

			checkPageAlloc(t, want, b)
		})
	}
}

func TestPageAllocExhaust(t *testing.T) {
	for _, npages := range []uintptr{1, 2, 3, 4, 5, 8, 16, 64, 1024, 1025, 2048, 2049} {
		npages := npages
		t.Run(fmt.Sprintf("%d", npages), func(t *testing.T) {
			// Construct b.
			bDesc := make(map[ChunkIdx][]BitRange)
			for i := ChunkIdx(0); i < 4; i++ {
				bDesc[BaseChunkIdx+i] = []BitRange{}
			}
			b := NewPageAlloc(bDesc, nil)
			defer FreePageAlloc(b)

			// Allocate into b with npages until we've exhausted the heap.
			nAlloc := (PallocChunkPages * 4) / int(npages)
			for i := 0; i < nAlloc; i++ {
				addr := PageBase(BaseChunkIdx, uint(i)*uint(npages))
				if a, _ := b.Alloc(npages); a != addr {
					t.Fatalf("bad alloc #%d: want 0x%x, got 0x%x", i+1, addr, a)
				}
			}

			// Check to make sure the next allocation fails.
			if a, _ := b.Alloc(npages); a != 0 {
				t.Fatalf("bad alloc #%d: want 0, got 0x%x", nAlloc, a)
			}

			// Construct what we want the heap to look like now.
			allocPages := nAlloc * int(npages)
			wantDesc := make(map[ChunkIdx][]BitRange)
			for i := ChunkIdx(0); i < 4; i++ {
				if allocPages >= PallocChunkPages {
					wantDesc[BaseChunkIdx+i] = []BitRange{{0, PallocChunkPages}}
					allocPages -= PallocChunkPages
				} else if allocPages > 0 {
					wantDesc[BaseChunkIdx+i] = []BitRange{{0, uint(allocPages)}}
					allocPages = 0
				} else {
					wantDesc[BaseChunkIdx+i] = []BitRange{}
				}
			}
			want := NewPageAlloc(wantDesc, nil)
			defer FreePageAlloc(want)

			// Check to make sure the heap b matches what we want.
			checkPageAlloc(t, want, b)
		})
	}
}

func TestPageAllocFree(t *testing.T) {
	tests := map[string]struct {
		before map[ChunkIdx][]BitRange
		after  map[ChunkIdx][]BitRange
		npages uintptr
		frees  []uintptr
	}{
		"Free1": {
			npages: 1,
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{0, PallocChunkPages}},
			},
			frees: []uintptr{
				PageBase(BaseChunkIdx, 0),
				PageBase(BaseChunkIdx, 1),
				PageBase(BaseChunkIdx, 2),
				PageBase(BaseChunkIdx, 3),
				PageBase(BaseChunkIdx, 4),
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{5, PallocChunkPages - 5}},
			},
		},
		"ManyArena1": {
			npages: 1,
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages}},
				BaseChunkIdx + 1: {{0, PallocChunkPages}},
				BaseChunkIdx + 2: {{0, PallocChunkPages}},
			},
			frees: []uintptr{
				PageBase(BaseChunkIdx, PallocChunkPages/2),
				PageBase(BaseChunkIdx+1, 0),
				PageBase(BaseChunkIdx+2, PallocChunkPages-1),
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages / 2}, {PallocChunkPages/2 + 1, PallocChunkPages/2 - 1}},
				BaseChunkIdx + 1: {{1, PallocChunkPages - 1}},
				BaseChunkIdx + 2: {{0, PallocChunkPages - 1}},
			},
		},
		"Free2": {
			npages: 2,
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{0, PallocChunkPages}},
			},
			frees: []uintptr{
				PageBase(BaseChunkIdx, 0),
				PageBase(BaseChunkIdx, 2),
				PageBase(BaseChunkIdx, 4),
				PageBase(BaseChunkIdx, 6),
				PageBase(BaseChunkIdx, 8),
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{10, PallocChunkPages - 10}},
			},
		},
		"Straddle2": {
			npages: 2,
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{PallocChunkPages - 1, 1}},
				BaseChunkIdx + 1: {{0, 1}},
			},
			frees: []uintptr{
				PageBase(BaseChunkIdx, PallocChunkPages-1),
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {},
				BaseChunkIdx + 1: {},
			},
		},
		"Free5": {
			npages: 5,
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{0, PallocChunkPages}},
			},
			frees: []uintptr{
				PageBase(BaseChunkIdx, 0),
				PageBase(BaseChunkIdx, 5),
				PageBase(BaseChunkIdx, 10),
				PageBase(BaseChunkIdx, 15),
				PageBase(BaseChunkIdx, 20),
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{25, PallocChunkPages - 25}},
			},
		},
		"Free64": {
			npages: 64,
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{0, PallocChunkPages}},
			},
			frees: []uintptr{
				PageBase(BaseChunkIdx, 0),
				PageBase(BaseChunkIdx, 64),
				PageBase(BaseChunkIdx, 128),
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{192, PallocChunkPages - 192}},
			},
		},
		"Free65": {
			npages: 65,
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{0, PallocChunkPages}},
			},
			frees: []uintptr{
				PageBase(BaseChunkIdx, 0),
				PageBase(BaseChunkIdx, 65),
				PageBase(BaseChunkIdx, 130),
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{195, PallocChunkPages - 195}},
			},
		},
		"FreePallocChunkPages": {
			npages: PallocChunkPages,
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {{0, PallocChunkPages}},
			},
			frees: []uintptr{
				PageBase(BaseChunkIdx, 0),
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx: {},
			},
		},
		"StraddlePallocChunkPages": {
			npages: PallocChunkPages,
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{PallocChunkPages / 2, PallocChunkPages / 2}},
				BaseChunkIdx + 1: {{0, PallocChunkPages / 2}},
			},
			frees: []uintptr{
				PageBase(BaseChunkIdx, PallocChunkPages/2),
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {},
				BaseChunkIdx + 1: {},
			},
		},
		"StraddlePallocChunkPages+1": {
			npages: PallocChunkPages + 1,
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages}},
				BaseChunkIdx + 1: {{0, PallocChunkPages}},
			},
			frees: []uintptr{
				PageBase(BaseChunkIdx, PallocChunkPages/2),
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages / 2}},
				BaseChunkIdx + 1: {{PallocChunkPages/2 + 1, PallocChunkPages/2 - 1}},
			},
		},
		"FreePallocChunkPages*2": {
			npages: PallocChunkPages * 2,
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages}},
				BaseChunkIdx + 1: {{0, PallocChunkPages}},
			},
			frees: []uintptr{
				PageBase(BaseChunkIdx, 0),
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {},
				BaseChunkIdx + 1: {},
			},
		},
		"StraddlePallocChunkPages*2": {
			npages: PallocChunkPages * 2,
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages}},
				BaseChunkIdx + 1: {{0, PallocChunkPages}},
				BaseChunkIdx + 2: {{0, PallocChunkPages}},
			},
			frees: []uintptr{
				PageBase(BaseChunkIdx, PallocChunkPages/2),
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages / 2}},
				BaseChunkIdx + 1: {},
				BaseChunkIdx + 2: {{PallocChunkPages / 2, PallocChunkPages / 2}},
			},
		},
		"AllFreePallocChunkPages*7+5": {
			npages: PallocChunkPages*7 + 5,
			before: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {{0, PallocChunkPages}},
				BaseChunkIdx + 1: {{0, PallocChunkPages}},
				BaseChunkIdx + 2: {{0, PallocChunkPages}},
				BaseChunkIdx + 3: {{0, PallocChunkPages}},
				BaseChunkIdx + 4: {{0, PallocChunkPages}},
				BaseChunkIdx + 5: {{0, PallocChunkPages}},
				BaseChunkIdx + 6: {{0, PallocChunkPages}},
				BaseChunkIdx + 7: {{0, PallocChunkPages}},
			},
			frees: []uintptr{
				PageBase(BaseChunkIdx, 0),
			},
			after: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {},
				BaseChunkIdx + 1: {},
				BaseChunkIdx + 2: {},
				BaseChunkIdx + 3: {},
				BaseChunkIdx + 4: {},
				BaseChunkIdx + 5: {},
				BaseChunkIdx + 6: {},
				BaseChunkIdx + 7: {{5, PallocChunkPages - 5}},
			},
		},
	}
	for name, v := range tests {
		v := v
		t.Run(name, func(t *testing.T) {
			b := NewPageAlloc(v.before, nil)
			defer FreePageAlloc(b)

			for _, addr := range v.frees {
				b.Free(addr, v.npages)
			}
			want := NewPageAlloc(v.after, nil)
			defer FreePageAlloc(want)

			checkPageAlloc(t, want, b)
		})
	}
}

func TestPageAllocAllocAndFree(t *testing.T) {
	type hit struct {
		alloc  bool
		npages uintptr
		base   uintptr
	}
	tests := map[string]struct {
		init map[ChunkIdx][]BitRange
		hits []hit
	}{
		// TODO(mknyszek): Write more tests here.
		"Chunks8": {
			init: map[ChunkIdx][]BitRange{
				BaseChunkIdx:     {},
				BaseChunkIdx + 1: {},
				BaseChunkIdx + 2: {},
				BaseChunkIdx + 3: {},
				BaseChunkIdx + 4: {},
				BaseChunkIdx + 5: {},
				BaseChunkIdx + 6: {},
				BaseChunkIdx + 7: {},
			},
			hits: []hit{
				{true, PallocChunkPages * 8, PageBase(BaseChunkIdx, 0)},
				{false, PallocChunkPages * 8, PageBase(BaseChunkIdx, 0)},
				{true, PallocChunkPages * 8, PageBase(BaseChunkIdx, 0)},
				{false, PallocChunkPages * 8, PageBase(BaseChunkIdx, 0)},
				{true, PallocChunkPages * 8, PageBase(BaseChunkIdx, 0)},
				{false, PallocChunkPages * 8, PageBase(BaseChunkIdx, 0)},
				{true, 1, PageBase(BaseChunkIdx, 0)},
				{false, 1, PageBase(BaseChunkIdx, 0)},
				{true, PallocChunkPages * 8, PageBase(BaseChunkIdx, 0)},
			},
		},
	}
	for name, v := range tests {
		v := v
		t.Run(name, func(t *testing.T) {
			b := NewPageAlloc(v.init, nil)
			defer FreePageAlloc(b)

			for iter, i := range v.hits {
				if i.alloc {
					if a, _ := b.Alloc(i.npages); a != i.base {
						t.Fatalf("bad alloc #%d: want 0x%x, got 0x%x", iter+1, i.base, a)
					}
				} else {
					b.Free(i.base, i.npages)
				}
			}
		})
	}
}

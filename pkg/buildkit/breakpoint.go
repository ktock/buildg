package buildkit

import (
	"context"
	"fmt"
	"sort"

	"github.com/moby/buildkit/solver/pb"
)

type BreakpointHandler func(ctx context.Context, bCtx BreakContext) error

type BreakContext struct {
	Handler *Handler
	Info    *RegisteredStatus
	Locs    []*Location
	Hits    map[string]BreakpointInfo
}

type BreakpointInfo struct {
	Description string
	Hits        []*Location
}

type Breakpoint interface {
	isTarget(ctx context.Context, status *RegisteredStatus, locs []*Location) (yes bool, description string, hitLocations []*Location, err error)
	IsMarked(source *pb.SourceInfo, line int64) bool
	String() string
}

func NewBreakpoints() *Breakpoints {
	return &Breakpoints{
		breakpoints: make(map[string]Breakpoint),
	}
}

type Breakpoints struct {
	breakpoints     map[string]Breakpoint
	breakpointIndex int
}

func (b *Breakpoints) Add(key string, bp Breakpoint) (string, error) {
	if b.breakpoints == nil {
		b.breakpoints = make(map[string]Breakpoint)
	}
	if key == "" {
		currentIdx := b.breakpointIndex
		b.breakpointIndex++
		key = fmt.Sprintf("%d", currentIdx)
	}
	if _, ok := b.breakpoints[key]; ok {
		return "", fmt.Errorf("breakpoint %q already exists: %v", key, b)
	}
	b.breakpoints[key] = bp
	return key, nil
}

func (b *Breakpoints) Get(key string) (Breakpoint, bool) {
	bp, ok := b.breakpoints[key]
	return bp, ok
}

func (b *Breakpoints) Clear(key string) {
	delete(b.breakpoints, key)
}

func (b *Breakpoints) ClearAll() {
	b.breakpoints = nil
	b.breakpointIndex = 0
}

func (b *Breakpoints) ForEach(f func(key string, bp Breakpoint) bool) {
	var keys []string
	for k := range b.breakpoints {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !f(k, b.breakpoints[k]) {
			return
		}
	}
}

func NewOnFailBreakpoint() Breakpoint {
	return &onFailBreakpoint{}
}

type onFailBreakpoint struct{}

func (b *onFailBreakpoint) isTarget(_ context.Context, status *RegisteredStatus, locs []*Location) (bool, string, []*Location, error) {
	return status.Err != nil, fmt.Sprintf("caught error %v", status.Err), locs, nil
}

func (b *onFailBreakpoint) String() string {
	return "breaks on fail"
}

func (b *onFailBreakpoint) IsMarked(_ *pb.SourceInfo, _ int64) bool {
	return false
}

func NewLineBreakpoint(filename string, line int64) Breakpoint {
	return &lineBreakpoint{filename, line}
}

type lineBreakpoint struct {
	filename string
	line     int64
}

func (b *lineBreakpoint) isTarget(_ context.Context, _ *RegisteredStatus, locs []*Location) (bool, string, []*Location, error) {
	var hits []*Location
	var found bool
	for _, loc := range locs {
		if loc.Source.Filename != b.filename {
			continue
		}
		for _, r := range loc.Ranges {
			if int64(r.Start.Line) <= b.line && b.line <= int64(r.End.Line) {
				hits = append(hits, &Location{
					Source: loc.Source,
					Ranges: []*pb.Range{r},
				})
				found = true
			}
		}
	}
	if found {
		return true, "reached " + b.String(), hits, nil
	}
	return false, "", nil, nil
}

func (b *lineBreakpoint) String() string {
	return fmt.Sprintf("line: %s:%d", b.filename, b.line)
}

func (b *lineBreakpoint) IsMarked(source *pb.SourceInfo, line int64) bool {
	return source.Filename == b.filename && line == b.line
}

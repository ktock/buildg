package buildkit

import (
	"fmt"

	"github.com/moby/buildkit/solver/pb"
)

type Location struct {
	Source *pb.SourceInfo
	Ranges []*pb.Range
}

func (l *Location) String() string {
	return fmt.Sprintf("%q %+v", l.Source.Filename, l.Ranges)
}

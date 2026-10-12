package git2

import (
	"bytes"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Signature identifies who authored or committed a change, and when.
//
// Unlike go-git, which reads a time zone such as -0030 as +0030, When keeps
// the zone's sign.
type Signature struct {
	Name  string
	Email string
	When  time.Time
}

// zones caches fixed-offset locations by offset in seconds. In practice
// offsets come from a small set, and time.FixedZone allocates.
var zones sync.Map // int -> *time.Location

func zone(offset int) *time.Location {
	if l, ok := zones.Load(offset); ok {
		return l.(*time.Location)
	}
	l, _ := zones.LoadOrStore(offset, time.FixedZone("", offset))
	return l.(*time.Location)
}

// decode parses "Name <email> 1234567890 +0100", aliasing v for strings.
// Malformed input leaves the missing pieces zero instead of failing.
func (s *Signature) decode(v string) {
	open := strings.LastIndexByte(v, '<')
	closeIdx := strings.LastIndexByte(v, '>')
	if open == -1 || closeIdx == -1 || closeIdx < open {
		return
	}
	s.Name = strings.Trim(v[:open], " ")
	s.Email = v[open+1 : closeIdx]
	if closeIdx+2 < len(v) {
		s.decodeTime(v[closeIdx+2:])
	}
}

// signatureTime returns the Unix time of the When that decode would read
// from b, without building the name and email.
func signatureTime(b []byte) int64 {
	var s Signature
	open := bytes.LastIndexByte(b, '<')
	closeIdx := bytes.LastIndexByte(b, '>')
	if open != -1 && closeIdx != -1 && closeIdx >= open && closeIdx+2 < len(b) {
		s.decodeTime(string(b[closeIdx+2:]))
	}
	return s.When.Unix()
}

// decodeTime parses "1234567890 +0100". A timestamp that does not parse
// leaves When zero, and a malformed or missing time zone leaves it in UTC.
// Zones such as -0030 keep their sign, as in git.
func (s *Signature) decodeTime(v string) {
	sp := strings.IndexByte(v, ' ')
	if sp == -1 {
		sp = len(v)
	}
	ts, err := strconv.ParseInt(v[:sp], 10, 64)
	if err != nil {
		return
	}
	s.When = time.Unix(ts, 0).In(time.UTC)
	if sp+1+5 > len(v) {
		return
	}
	tz := v[sp+1 : sp+1+5]
	h, err1 := strconv.ParseInt(tz[:3], 10, 64)
	m, err2 := strconv.ParseInt(tz[3:], 10, 64)
	if err1 != nil || err2 != nil {
		return
	}
	if tz[0] == '-' {
		m = -m
	}
	s.When = s.When.In(zone(int(h*3600 + m*60)))
}

func (s Signature) String() string {
	return s.Name + " <" + s.Email + ">"
}

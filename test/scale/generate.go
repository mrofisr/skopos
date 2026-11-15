package scale

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"strconv"
)

// Dataset is the deterministic shape and digest of one generated dataset.
type Dataset struct {
	Profile       Profile
	Seed          int64
	MetricSeries  int
	MetricSamples int
	LogLines      int
	Spans         int
}

// Generate validates the profile and returns its deterministic dataset.
func Generate(profile Profile, seed int64) (Dataset, error) {
	if err := profile.Validate(); err != nil {
		return Dataset{}, err
	}
	return Dataset{
		Profile:       profile,
		Seed:          seed,
		MetricSeries:  profile.Series,
		MetricSamples: profile.Series * profile.Samples,
		LogLines:      profile.Logs,
		Spans:         profile.Spans,
	}, nil
}

// Hash returns a stable digest of the canonical row stream. It hashes every
// series identity and every sample value in profile order, so the same profile
// and seed always produce the same digest and a different seed changes it.
func (d Dataset) Hash() string {
	h := fnv.New64a()
	var buf [8]byte
	write := func(value string) {
		_, _ = h.Write([]byte(value))
		_, _ = h.Write([]byte{0})
	}
	write(d.Profile.Name)
	write(strconv.FormatInt(int64(d.Profile.Range), 10))
	write(strconv.FormatInt(int64(d.Profile.Step), 10))
	write(strconv.Itoa(d.Profile.Logs))
	write(strconv.Itoa(d.Profile.Spans))
	binary.LittleEndian.PutUint64(buf[:], uint64(d.Seed))
	_, _ = h.Write(buf[:])
	for series := 0; series < d.Profile.Series; series++ {
		write(strconv.Itoa(series))
		write(strconv.Itoa(series % d.Profile.Services))
		for sample := 0; sample < d.Profile.Samples; sample++ {
			binary.LittleEndian.PutUint64(buf[:], mix64(uint64(d.Seed), uint64(series), uint64(sample)))
			_, _ = h.Write(buf[:])
		}
	}
	return fmt.Sprintf("%016x", h.Sum64())
}

// mix64 is the splitmix64 finalizer with the seed, series, and sample mixed in.
// It is deterministic across Go versions and platforms.
func mix64(seed, series, sample uint64) uint64 {
	x := seed + series*0x9e3779b97f4a7c15 + sample*0xbf58476d1ce4e5b9
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

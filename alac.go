// Apple Lossless (ALAC) decoder
package alac

import (
	"fmt"
)

// Config holds ALAC decoder configuration parameters.
type Config struct {
	SampleRate  int // e.g., 44100, 48000, 96000
	SampleSize  int // bits per sample: 16 or 24
	NumChannels int // 1 (mono) or 2 (stereo)
	FrameSize   int // max samples per frame, typically 4096
}

// DefaultConfig returns the default configuration (16-bit stereo 44.1kHz).
func DefaultConfig() Config {
	return Config{
		SampleRate:  44100,
		SampleSize:  16,
		NumChannels: 2,
		FrameSize:   352,
	}
}

// NewWithConfig creates an ALAC decoder with the specified configuration.
func NewWithConfig(cfg Config) (*Alac, error) {
	a := create_alac(cfg.SampleSize, cfg.NumChannels)
	if a == nil {
		return nil, fmt.Errorf("can't create alac decoder")
	}

	a.setinfo_max_samples_per_frame = uint32(cfg.FrameSize)
	a.setinfo_7a = 0
	a.setinfo_sample_size = uint8(cfg.SampleSize)
	a.setinfo_rice_historymult = 40
	a.setinfo_rice_initialhistory = 10
	a.setinfo_rice_kmodifier = 14
	a.setinfo_7f = 2
	a.setinfo_80 = 255
	a.setinfo_82 = 0
	a.setinfo_86 = 0
	a.setinfo_8a_rate = uint32(cfg.SampleRate)

	a.allocateBuffers()
	return a, nil
}

// New creates an ALAC decoder with default settings (16-bit stereo 44.1kHz).
func New() (*Alac, error) {
	return NewWithConfig(DefaultConfig())
}

func (a *Alac) Decode(f []byte) []byte {
	return a.decodeFrame(f)
}

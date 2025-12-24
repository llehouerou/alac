//go:build ignore

// This script generates test data for ALAC decoder testing.
// Run with: go run testdata/generate.go
//
// Requirements: FFmpeg must be installed and available in PATH.

package main

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
)

// Real audio samples from Librivox (public domain)
// Source WAV files are stored in testdata/samples/
var realAudioSamples = []struct {
	name   string
	source string // relative to testdata/
}{
	// "Jane Eyre" by Charlotte Bronte, Chapter 1 (5 seconds)
	// LibriVox recording (version 3)
	{"librivox_jane_eyre", "samples/jane_eyre_5s.wav"},
	// "The Count of Monte Cristo" by Alexandre Dumas, Chapter 1 (5 seconds)
	// LibriVox recording
	{"librivox_monte_cristo", "samples/monte_cristo_5s.wav"},
}

// TestConfig describes a test configuration
type TestConfig struct {
	SampleRate  int `json:"sample_rate"`
	SampleSize  int `json:"sample_size"`
	NumChannels int `json:"num_channels"`
	FrameSize   int `json:"frame_size"`
}

var configs = []TestConfig{
	{44100, 16, 1, 4096},
	{44100, 16, 2, 4096},
	{44100, 24, 1, 4096},
	{44100, 24, 2, 4096},
	{48000, 16, 1, 4096},
	{48000, 16, 2, 4096},
	{48000, 24, 1, 4096},
	{48000, 24, 2, 4096},
	{96000, 16, 1, 4096},
	{96000, 16, 2, 4096},
	{96000, 24, 1, 4096},
	{96000, 24, 2, 4096},
}

var audioTypes = []string{"silence", "sine1k", "sweep", "noise", "whitenoise"}

func main() {
	if err := checkFFmpeg(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintf(os.Stderr, "Please install FFmpeg: https://ffmpeg.org/download.html\n")
		os.Exit(1)
	}

	baseDir := filepath.Join("testdata", "generated")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating directory: %v\n", err)
		os.Exit(1)
	}

	// Generate synthetic audio for each config
	for _, cfg := range configs {
		dirName := fmt.Sprintf("%d_%d_%s", cfg.SampleRate, cfg.SampleSize, channelName(cfg.NumChannels))
		dir := filepath.Join(baseDir, dirName)
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating directory %s: %v\n", dir, err)
			continue
		}

		for _, audioType := range audioTypes {
			if err := generateTestCase(dir, audioType, cfg); err != nil {
				fmt.Fprintf(os.Stderr, "Error generating %s/%s: %v\n", dirName, audioType, err)
			} else {
				fmt.Printf("Generated %s/%s\n", dirName, audioType)
			}
		}
	}

	// Generate real audio samples from Librivox
	realDir := filepath.Join(baseDir, "real_audio")
	if err := os.MkdirAll(realDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating real audio directory: %v\n", err)
	} else {
		for _, sample := range realAudioSamples {
			sourcePath := filepath.Join("testdata", sample.source)
			if err := generateRealAudioTestCase(realDir, sample.name, sourcePath); err != nil {
				fmt.Fprintf(os.Stderr, "Error generating real audio %s: %v\n", sample.name, err)
			} else {
				fmt.Printf("Generated real_audio/%s\n", sample.name)
			}
		}
	}

	fmt.Println("Done!")
}

func checkFFmpeg() error {
	cmd := exec.Command("ffmpeg", "-version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg not found: %w", err)
	}
	return nil
}

func channelName(n int) string {
	if n == 1 {
		return "mono"
	}
	return "stereo"
}

func generateTestCase(dir, audioType string, cfg TestConfig) error {
	wavPath := filepath.Join(dir, audioType+".wav")
	m4aPath := filepath.Join(dir, audioType+".m4a")
	rawPath := filepath.Join(dir, audioType+".raw")
	jsonPath := filepath.Join(dir, audioType+".json")

	// Skip if all files exist
	if fileExists(wavPath) && fileExists(m4aPath) && fileExists(rawPath) && fileExists(jsonPath) {
		return nil
	}

	// Generate WAV
	duration := 0.5 // 500ms of audio
	samples := int(float64(cfg.SampleRate) * duration)
	if err := generateWAV(wavPath, audioType, cfg, samples); err != nil {
		return fmt.Errorf("generating WAV: %w", err)
	}

	// Encode to ALAC using FFmpeg
	if err := encodeALAC(wavPath, m4aPath); err != nil {
		return fmt.Errorf("encoding ALAC: %w", err)
	}

	// Decode to raw PCM (reference output)
	if err := decodeToRaw(m4aPath, rawPath, cfg); err != nil {
		return fmt.Errorf("decoding to raw: %w", err)
	}

	// Write config JSON
	if err := writeConfig(jsonPath, cfg); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func generateWAV(path, audioType string, cfg TestConfig, samples int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	bytesPerSample := cfg.SampleSize / 8
	dataSize := samples * cfg.NumChannels * bytesPerSample

	// Write WAV header
	if err := writeWAVHeader(f, cfg, dataSize); err != nil {
		return err
	}

	// Generate and write samples
	for i := 0; i < samples; i++ {
		for ch := 0; ch < cfg.NumChannels; ch++ {
			var sample float64
			switch audioType {
			case "silence":
				sample = 0
			case "sine1k":
				sample = math.Sin(2 * math.Pi * 1000 * float64(i) / float64(cfg.SampleRate))
			case "sweep":
				// Logarithmic sweep from 20Hz to 20kHz
				t := float64(i) / float64(samples)
				freq := 20 * math.Pow(1000, t) // 20 to 20000 Hz
				phase := 2 * math.Pi * freq * float64(i) / float64(cfg.SampleRate)
				sample = math.Sin(phase)
			case "noise":
				// Simple pseudo-random noise using LCG
				seed := uint32(i*cfg.NumChannels + ch + 12345)
				seed = seed*1103515245 + 12345
				sample = float64(int32(seed))/float64(math.MaxInt32) * 0.5
			case "whitenoise":
				// True random noise using crypto/rand - high entropy may trigger uncompressed frames
				var b [2]byte
				rand.Read(b[:])
				sample = float64(int16(binary.LittleEndian.Uint16(b[:]))) / 32768.0
			}

			// Scale to sample size and write
			if cfg.SampleSize == 16 {
				val := int16(sample * 32767)
				binary.Write(f, binary.LittleEndian, val)
			} else if cfg.SampleSize == 24 {
				val := int32(sample * 8388607)
				// Write 24-bit as 3 bytes
				f.Write([]byte{byte(val), byte(val >> 8), byte(val >> 16)})
			}
		}
	}

	return nil
}

func writeWAVHeader(f *os.File, cfg TestConfig, dataSize int) error {
	bytesPerSample := cfg.SampleSize / 8
	blockAlign := cfg.NumChannels * bytesPerSample
	byteRate := cfg.SampleRate * blockAlign

	// RIFF header
	f.Write([]byte("RIFF"))
	binary.Write(f, binary.LittleEndian, uint32(36+dataSize))
	f.Write([]byte("WAVE"))

	// fmt chunk
	f.Write([]byte("fmt "))
	binary.Write(f, binary.LittleEndian, uint32(16))         // chunk size
	binary.Write(f, binary.LittleEndian, uint16(1))          // audio format (PCM)
	binary.Write(f, binary.LittleEndian, uint16(cfg.NumChannels))
	binary.Write(f, binary.LittleEndian, uint32(cfg.SampleRate))
	binary.Write(f, binary.LittleEndian, uint32(byteRate))
	binary.Write(f, binary.LittleEndian, uint16(blockAlign))
	binary.Write(f, binary.LittleEndian, uint16(cfg.SampleSize))

	// data chunk
	f.Write([]byte("data"))
	binary.Write(f, binary.LittleEndian, uint32(dataSize))

	return nil
}

func encodeALAC(wavPath, m4aPath string) error {
	cmd := exec.Command("ffmpeg", "-y", "-i", wavPath, "-c:a", "alac", m4aPath)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func decodeToRaw(m4aPath, rawPath string, cfg TestConfig) error {
	// Determine PCM format
	var format string
	if cfg.SampleSize == 16 {
		format = "s16le"
	} else {
		format = "s24le"
	}

	cmd := exec.Command("ffmpeg", "-y", "-i", m4aPath, "-f", format, "-acodec", "pcm_"+format, rawPath)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func writeConfig(path string, cfg TestConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// generateRealAudioTestCase processes a local WAV sample into ALAC test data
func generateRealAudioTestCase(dir, name, wavSourcePath string) error {
	m4aPath := filepath.Join(dir, name+".m4a")
	rawPath := filepath.Join(dir, name+".raw")
	jsonPath := filepath.Join(dir, name+".json")

	// Skip if all files exist
	if fileExists(m4aPath) && fileExists(rawPath) && fileExists(jsonPath) {
		return nil
	}

	// Check source exists
	if !fileExists(wavSourcePath) {
		return fmt.Errorf("source file not found: %s", wavSourcePath)
	}

	// Encode to ALAC
	if err := encodeALAC(wavSourcePath, m4aPath); err != nil {
		return fmt.Errorf("encoding ALAC: %w", err)
	}

	// Decode to raw PCM
	cfg := TestConfig{
		SampleRate:  44100,
		SampleSize:  16,
		NumChannels: 2,
		FrameSize:   4096,
	}
	if err := decodeToRaw(m4aPath, rawPath, cfg); err != nil {
		return fmt.Errorf("decoding to raw: %w", err)
	}

	// Write config
	if err := writeConfig(jsonPath, cfg); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	return nil
}

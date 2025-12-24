package alac

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestConfig matches the JSON config from generate.go
type testConfigJSON struct {
	SampleRate  int `json:"sample_rate"`
	SampleSize  int `json:"sample_size"`
	NumChannels int `json:"num_channels"`
	FrameSize   int `json:"frame_size"`
}

func TestMatrix(t *testing.T) {
	baseDir := "testdata/generated"

	entries, err := os.ReadDir(baseDir)
	if os.IsNotExist(err) {
		t.Skip("Test data not generated. Run: go run testdata/generate.go (requires FFmpeg)")
	}
	if err != nil {
		t.Fatalf("Failed to read test data directory: %v", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		configDir := filepath.Join(baseDir, entry.Name())
		audioFiles, err := filepath.Glob(filepath.Join(configDir, "*.m4a"))
		if err != nil {
			t.Errorf("Failed to glob %s: %v", configDir, err)
			continue
		}

		for _, m4aPath := range audioFiles {
			baseName := m4aPath[:len(m4aPath)-4] // strip .m4a
			testName := filepath.Base(configDir) + "/" + filepath.Base(baseName)

			t.Run(testName, func(t *testing.T) {
				runMatrixTest(t, baseName)
			})
		}
	}
}

func runMatrixTest(t *testing.T, baseName string) {
	// Load config
	jsonPath := baseName + ".json"
	jsonData, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("Failed to read config: %v", err)
	}

	var cfg testConfigJSON
	if err := json.Unmarshal(jsonData, &cfg); err != nil {
		t.Fatalf("Failed to parse config: %v", err)
	}

	// Load expected output
	rawPath := baseName + ".raw"
	expected, err := os.ReadFile(rawPath)
	if err != nil {
		t.Fatalf("Failed to read expected output: %v", err)
	}

	// Parse M4A and extract ALAC frames
	m4aPath := baseName + ".m4a"
	frames, alacConfig, err := parseM4A(m4aPath)
	if err != nil {
		t.Fatalf("Failed to parse M4A: %v", err)
	}

	// Create decoder with config from M4A
	decoder, err := NewWithConfig(Config{
		SampleRate:  cfg.SampleRate,
		SampleSize:  cfg.SampleSize,
		NumChannels: cfg.NumChannels,
		FrameSize:   alacConfig.frameSize,
	})
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	// Decode all frames
	var decoded []byte
	for i, frame := range frames {
		result := decoder.Decode(frame)
		if result == nil {
			t.Fatalf("Frame %d: decode returned nil", i)
		}
		decoded = append(decoded, result...)
	}

	// Compare with tolerance
	if err := compareSamples(decoded, expected, cfg.SampleSize); err != nil {
		t.Errorf("Sample mismatch: %v", err)
	}
}

func compareSamples(got, want []byte, sampleSize int) error {
	if len(got) != len(want) {
		return fmt.Errorf("length mismatch: got %d, want %d", len(got), len(want))
	}

	bytesPerSample := sampleSize / 8

	for i := 0; i < len(got); i += bytesPerSample {
		var g, w int32

		if sampleSize == 16 {
			g = int32(int16(binary.LittleEndian.Uint16(got[i:])))
			w = int32(int16(binary.LittleEndian.Uint16(want[i:])))
		} else if sampleSize == 24 {
			// 24-bit little endian
			gb := got[i : i+3]
			wb := want[i : i+3]
			g = int32(gb[0]) | int32(gb[1])<<8 | int32(int8(gb[2]))<<16
			w = int32(wb[0]) | int32(wb[1])<<8 | int32(int8(wb[2]))<<16
		}

		diff := g - w
		if diff < 0 {
			diff = -diff
		}
		if diff > 1 {
			sampleIdx := i / bytesPerSample
			return fmt.Errorf("sample %d: got %d, want %d (diff %d)", sampleIdx, g, w, diff)
		}
	}

	return nil
}

// M4A parsing structures

type alacConfigInfo struct {
	frameSize   int
	sampleRate  int
	sampleSize  int
	numChannels int
}

func parseM4A(path string) ([][]byte, alacConfigInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, alacConfigInfo{}, err
	}
	defer f.Close()

	// Parse atoms to find moov and mdat
	var moovData, mdatData []byte
	var mdatOffset int64

	for {
		offset, _ := f.Seek(0, io.SeekCurrent)
		size, atomType, err := readAtomHeader(f)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, alacConfigInfo{}, err
		}

		dataSize := int64(size) - 8
		if size == 1 {
			// Extended size
			var extSize uint64
			binary.Read(f, binary.BigEndian, &extSize)
			dataSize = int64(extSize) - 16
		}

		switch atomType {
		case "moov":
			moovData = make([]byte, dataSize)
			if _, err := io.ReadFull(f, moovData); err != nil {
				return nil, alacConfigInfo{}, err
			}
		case "mdat":
			mdatOffset = offset + 8
			if size == 1 {
				mdatOffset += 8
			}
			mdatData = make([]byte, dataSize)
			if _, err := io.ReadFull(f, mdatData); err != nil {
				return nil, alacConfigInfo{}, err
			}
		default:
			f.Seek(dataSize, io.SeekCurrent)
		}
	}

	if moovData == nil {
		return nil, alacConfigInfo{}, fmt.Errorf("moov atom not found")
	}
	if mdatData == nil {
		return nil, alacConfigInfo{}, fmt.Errorf("mdat atom not found")
	}

	// Parse moov to get sample table info
	stbl, err := findAtomPath(moovData, []string{"trak", "mdia", "minf", "stbl"})
	if err != nil {
		return nil, alacConfigInfo{}, fmt.Errorf("stbl not found: %w", err)
	}

	// Get sample sizes from stsz
	stsz, err := findAtom(stbl, "stsz")
	if err != nil {
		return nil, alacConfigInfo{}, fmt.Errorf("stsz not found: %w", err)
	}
	sampleSizes := parseSTSZ(stsz)

	// Get chunk offsets from stco or co64
	var chunkOffsets []int64
	if stco, err := findAtom(stbl, "stco"); err == nil {
		chunkOffsets = parseSTCO(stco)
	} else if co64, err := findAtom(stbl, "co64"); err == nil {
		chunkOffsets = parseCO64(co64)
	} else {
		return nil, alacConfigInfo{}, fmt.Errorf("stco/co64 not found")
	}

	// Get sample-to-chunk mapping from stsc
	stsc, err := findAtom(stbl, "stsc")
	if err != nil {
		return nil, alacConfigInfo{}, fmt.Errorf("stsc not found: %w", err)
	}
	stscEntries := parseSTSC(stsc)

	// Get ALAC config from stsd
	stsd, err := findAtom(stbl, "stsd")
	if err != nil {
		return nil, alacConfigInfo{}, fmt.Errorf("stsd not found: %w", err)
	}
	alacCfg, err := parseALACConfig(stsd)
	if err != nil {
		return nil, alacConfigInfo{}, fmt.Errorf("failed to parse ALAC config: %w", err)
	}

	// Extract samples from mdat
	frames := extractSamples(mdatData, mdatOffset, sampleSizes, chunkOffsets, stscEntries)

	return frames, alacCfg, nil
}

func readAtomHeader(r io.Reader) (uint32, string, error) {
	var size uint32
	if err := binary.Read(r, binary.BigEndian, &size); err != nil {
		return 0, "", err
	}
	typeBytes := make([]byte, 4)
	if _, err := io.ReadFull(r, typeBytes); err != nil {
		return 0, "", err
	}
	return size, string(typeBytes), nil
}

func findAtom(data []byte, name string) ([]byte, error) {
	offset := 0
	for offset+8 <= len(data) {
		size := int(binary.BigEndian.Uint32(data[offset:]))
		atomType := string(data[offset+4 : offset+8])

		if size < 8 {
			break
		}
		if offset+size > len(data) {
			size = len(data) - offset
		}

		if atomType == name {
			return data[offset+8 : offset+size], nil
		}
		offset += size
	}
	return nil, fmt.Errorf("atom %s not found", name)
}

func findAtomPath(data []byte, path []string) ([]byte, error) {
	current := data
	for _, name := range path {
		var err error
		current, err = findAtom(current, name)
		if err != nil {
			return nil, err
		}
	}
	return current, nil
}

func parseSTSZ(data []byte) []int {
	if len(data) < 12 {
		return nil
	}
	// version(1) + flags(3) + sample_size(4) + sample_count(4)
	sampleSize := binary.BigEndian.Uint32(data[4:8])
	sampleCount := int(binary.BigEndian.Uint32(data[8:12]))

	sizes := make([]int, sampleCount)
	if sampleSize != 0 {
		// Fixed size
		for i := range sizes {
			sizes[i] = int(sampleSize)
		}
	} else {
		// Variable sizes
		for i := 0; i < sampleCount && 12+i*4+4 <= len(data); i++ {
			sizes[i] = int(binary.BigEndian.Uint32(data[12+i*4:]))
		}
	}
	return sizes
}

func parseSTCO(data []byte) []int64 {
	if len(data) < 8 {
		return nil
	}
	count := int(binary.BigEndian.Uint32(data[4:8]))
	offsets := make([]int64, count)
	for i := 0; i < count && 8+i*4+4 <= len(data); i++ {
		offsets[i] = int64(binary.BigEndian.Uint32(data[8+i*4:]))
	}
	return offsets
}

func parseCO64(data []byte) []int64 {
	if len(data) < 8 {
		return nil
	}
	count := int(binary.BigEndian.Uint32(data[4:8]))
	offsets := make([]int64, count)
	for i := 0; i < count && 8+i*8+8 <= len(data); i++ {
		offsets[i] = int64(binary.BigEndian.Uint64(data[8+i*8:]))
	}
	return offsets
}

type stscEntry struct {
	firstChunk      int
	samplesPerChunk int
}

func parseSTSC(data []byte) []stscEntry {
	if len(data) < 8 {
		return nil
	}
	count := int(binary.BigEndian.Uint32(data[4:8]))
	entries := make([]stscEntry, count)
	for i := 0; i < count && 8+i*12+12 <= len(data); i++ {
		offset := 8 + i*12
		entries[i] = stscEntry{
			firstChunk:      int(binary.BigEndian.Uint32(data[offset:])),
			samplesPerChunk: int(binary.BigEndian.Uint32(data[offset+4:])),
		}
	}
	return entries
}

func parseALACConfig(stsdData []byte) (alacConfigInfo, error) {
	// stsd: version(1) + flags(3) + entry_count(4) + entries...
	if len(stsdData) < 8 {
		return alacConfigInfo{}, fmt.Errorf("stsd too short")
	}

	// Skip to first entry
	offset := 8

	// Entry: size(4) + format(4) + reserved(6) + data_ref_index(2) + ...
	if offset+28 > len(stsdData) {
		return alacConfigInfo{}, fmt.Errorf("stsd entry too short")
	}

	// For audio: + version(2) + revision(2) + vendor(4) + channels(2) + sampleSize(2) + compressionID(2) + packetSize(2) + sampleRate(4)
	// Total header before codec-specific: 8 + 6 + 2 + 2 + 2 + 4 + 2 + 2 + 2 + 2 + 4 = 36 bytes

	if offset+36 > len(stsdData) {
		return alacConfigInfo{}, fmt.Errorf("audio sample entry too short")
	}

	numChannels := int(binary.BigEndian.Uint16(stsdData[offset+24:]))
	sampleSize := int(binary.BigEndian.Uint16(stsdData[offset+26:]))
	sampleRate := int(binary.BigEndian.Uint32(stsdData[offset+32:]) >> 16)

	// Look for alac atom inside the sample entry
	entrySize := int(binary.BigEndian.Uint32(stsdData[offset:]))
	if offset+entrySize > len(stsdData) {
		entrySize = len(stsdData) - offset
	}

	// Search for 'alac' sub-atom starting after the audio sample entry header
	alacAtomOffset := offset + 36
	for alacAtomOffset+8 <= offset+entrySize {
		atomSize := int(binary.BigEndian.Uint32(stsdData[alacAtomOffset:]))
		atomType := string(stsdData[alacAtomOffset+4 : alacAtomOffset+8])
		if atomSize < 8 {
			break
		}
		if atomType == "alac" && alacAtomOffset+atomSize <= offset+entrySize {
			// alac atom: size(4) + 'alac'(4) + version(4) + config...
			// Config: frameLength(4) + compatibleVersion(1) + bitDepth(1) + pb(1) + mb(1) + kb(1) + numChannels(1) + maxRun(2) + maxFrameBytes(4) + avgBitRate(4) + sampleRate(4)
			cfgOffset := alacAtomOffset + 12
			if cfgOffset+24 <= len(stsdData) {
				frameSize := int(binary.BigEndian.Uint32(stsdData[cfgOffset:]))
				return alacConfigInfo{
					frameSize:   frameSize,
					sampleRate:  sampleRate,
					sampleSize:  sampleSize,
					numChannels: numChannels,
				}, nil
			}
		}
		alacAtomOffset += atomSize
	}

	// Default frame size if not found
	return alacConfigInfo{
		frameSize:   4096,
		sampleRate:  sampleRate,
		sampleSize:  sampleSize,
		numChannels: numChannels,
	}, nil
}

func extractSamples(mdatData []byte, mdatOffset int64, sampleSizes []int, chunkOffsets []int64, stscEntries []stscEntry) [][]byte {
	var frames [][]byte
	sampleIdx := 0

	for chunkIdx, chunkOffset := range chunkOffsets {
		// Find how many samples in this chunk
		samplesInChunk := 1
		for i := len(stscEntries) - 1; i >= 0; i-- {
			if chunkIdx+1 >= stscEntries[i].firstChunk {
				samplesInChunk = stscEntries[i].samplesPerChunk
				break
			}
		}

		// Extract samples from this chunk
		offset := chunkOffset - mdatOffset
		for s := 0; s < samplesInChunk && sampleIdx < len(sampleSizes); s++ {
			size := sampleSizes[sampleIdx]
			if offset >= 0 && int(offset)+size <= len(mdatData) {
				frame := make([]byte, size)
				copy(frame, mdatData[int(offset):int(offset)+size])
				frames = append(frames, frame)
			}
			offset += int64(size)
			sampleIdx++
		}
	}

	return frames
}

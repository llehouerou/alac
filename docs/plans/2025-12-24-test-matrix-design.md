# ALAC Decoder Test Matrix Design

## Overview

Comprehensive testing of the ALAC decoder using license-free audio samples with full matrix coverage across sample rates, bit depths, and channel configurations.

## Test Matrix

| Sample Rate | Bit Depth | Channels |
|-------------|-----------|----------|
| 44100 Hz    | 16-bit    | mono     |
| 44100 Hz    | 16-bit    | stereo   |
| 44100 Hz    | 24-bit    | mono     |
| 44100 Hz    | 24-bit    | stereo   |
| 48000 Hz    | 16-bit    | mono     |
| 48000 Hz    | 16-bit    | stereo   |
| 48000 Hz    | 24-bit    | mono     |
| 48000 Hz    | 24-bit    | stereo   |
| 96000 Hz    | 16-bit    | mono     |
| 96000 Hz    | 16-bit    | stereo   |
| 96000 Hz    | 24-bit    | mono     |
| 96000 Hz    | 24-bit    | stereo   |

**Synthetic audio types for each config:**
- Silence (tests zero handling)
- 1kHz sine wave (basic tone)
- Frequency sweep 20Hz-20kHz (exercises full range)
- White noise (random data, stress test)

**Real audio:** 1-2 short public domain clips (speech, music) at 44.1kHz/16-bit stereo.

## File Structure

```
testdata/
├── generate.go          # go:generate script
├── generated/           # gitignored, created by generate.go
│   ├── 44100_16_mono/
│   │   ├── silence.m4a      # ALAC in M4A container
│   │   ├── silence.alac     # raw ALAC frames (extracted by FFmpeg)
│   │   ├── silence.raw      # expected PCM from FFmpeg
│   │   ├── silence.json     # codec params
│   │   ├── sine1k.m4a
│   │   ├── sine1k.alac
│   │   ├── sine1k.raw
│   │   ├── sine1k.json
│   │   └── ...
│   ├── 44100_16_stereo/
│   │   └── ...
│   └── ...
└── sources/             # real audio (downloaded)
```

## Generation Pipeline

1. `go generate ./testdata` runs `generate.go`
2. For each config in the matrix:
   - Generate WAV using Go (sine, sweep, noise via math)
   - Encode to ALAC with FFmpeg: `ffmpeg -i input.wav -c:a alac output.m4a`
   - Extract raw ALAC frames: `ffmpeg -i input.m4a -c:a copy -f alac output.alac`
   - Extract reference PCM: `ffmpeg -i output.m4a -f s16le output.raw`
   - Write codec params to `.json`
3. Skip generation if files already exist (cache)

**Dependencies:** FFmpeg must be installed. Generation fails gracefully with clear error if missing.

## Test Execution

1. Check if `testdata/generated/` exists, if not run `go generate`
2. For each test case:
   - Load config from `.json`
   - Initialize decoder with config
   - Decode `.alac` frames
   - Compare against `.raw` file

## Comparison Logic

- Tolerance: ±1 LSB to account for rounding differences between implementations
- On failure: report config, sample index, expected vs actual value
- Summary: pass/fail count per configuration

## Implementation Order

1. **Extend decoder API** - Add `NewWithConfig()` to support variable configurations
2. **Create generation script** - `testdata/generate.go` with synthetic WAV generation and FFmpeg calls
3. **Download real audio** - Fetch public domain clips from Librivox/archive.org
4. **Write test harness** - `alac_matrix_test.go` iterating over generated test cases
5. **CI integration** - Add FFmpeg dependency, run `go generate && go test`

## Reference Decoder

FFmpeg is used as the reference implementation for:
- Encoding source WAV to ALAC
- Extracting raw ALAC frames from M4A container
- Decoding ALAC to reference PCM for comparison

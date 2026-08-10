package main

import (
	"encoding/binary"
	"errors"
	"time"
)

type WAVInfo struct {
	Duration time.Duration
	DataSize uint32
}

func validateWAV(data []byte, maxDuration time.Duration) (WAVInfo, error) {
	var info WAVInfo
	if len(data) < 44 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return info, errors.New("asset must be a RIFF/WAVE file")
	}
	if int(binary.LittleEndian.Uint32(data[4:8]))+8 != len(data) {
		return info, errors.New("invalid RIFF size")
	}
	var formatCount, dataCount int
	pos := 12
	for pos+8 <= len(data) {
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		start, end := pos+8, pos+8+size
		if end < start || end > len(data) {
			return info, errors.New("truncated WAV chunk")
		}
		switch string(data[pos : pos+4]) {
		case "fmt ":
			formatCount++
			if formatCount != 1 || size != 16 {
				return info, errors.New("invalid WAV format chunk")
			}
			f := data[start:end]
			pcm := binary.LittleEndian.Uint16(f[0:2])
			channels := binary.LittleEndian.Uint16(f[2:4])
			rate := binary.LittleEndian.Uint32(f[4:8])
			byteRate := binary.LittleEndian.Uint32(f[8:12])
			align := binary.LittleEndian.Uint16(f[12:14])
			bits := binary.LittleEndian.Uint16(f[14:16])
			if pcm != 1 || channels != 1 || rate != 8000 ||
				byteRate != 16000 || align != 2 || bits != 16 {
				return info, errors.New("WAV must be PCM 16-bit mono at 8000 Hz")
			}
		case "data":
			dataCount++
			if dataCount != 1 {
				return info, errors.New("WAV must contain exactly one data chunk")
			}
			info.DataSize = uint32(size)
		}
		pos = end + size%2
	}
	if pos != len(data) {
		return info, errors.New("truncated WAV chunk header or padding")
	}
	if formatCount != 1 || dataCount != 1 {
		return info, errors.New("WAV must contain exactly one fmt and one data chunk")
	}
	if info.DataSize == 0 || info.DataSize%2 != 0 {
		return info, errors.New("WAV has invalid or empty audio data")
	}
	info.Duration = time.Duration(info.DataSize) * time.Second / 16000
	if maxDuration <= 0 || info.Duration > maxDuration {
		return info, errors.New("WAV duration exceeds configured cap")
	}
	return info, nil
}

package images

import (
	"bufio"
	"encoding/binary"
	"io"
)

const orientationTag = 0x0112

// Orientation liest die EXIF-Ausrichtung 1-8 aus einem JPEG. Handys speichern hochkant Aufgenommenes
// oft quer mit diesem Vermerk. Ohne oder mit kaputten Metadaten: 1, also ungedreht.
func Orientation(r io.Reader) int {
	br := bufio.NewReader(r)
	var soi [2]byte
	if _, err := io.ReadFull(br, soi[:]); err != nil || soi != [2]byte{0xFF, 0xD8} {
		return 1
	}
	for {
		marker, err := nextMarker(br)
		if err != nil {
			return 1
		}
		// Ab Start of Scan folgen Bilddaten, EXIF kommt davor
		if marker == 0xDA || marker == 0xD9 {
			return 1
		}
		var lenBuf [2]byte
		if _, err := io.ReadFull(br, lenBuf[:]); err != nil {
			return 1
		}
		size := int(binary.BigEndian.Uint16(lenBuf[:])) - 2
		if size < 0 {
			return 1
		}
		if marker != 0xE1 {
			if _, err := br.Discard(size); err != nil {
				return 1
			}
			continue
		}
		segment := make([]byte, size)
		if _, err := io.ReadFull(br, segment); err != nil {
			return 1
		}
		if len(segment) >= 6 && string(segment[:6]) == "Exif\x00\x00" {
			return tiffOrientation(segment[6:])
		}
		// Anderes APP1 (etwa XMP) - weitersuchen
	}
}

func nextMarker(br *bufio.Reader) (byte, error) {
	b, err := br.ReadByte()
	if err != nil {
		return 0, err
	}
	if b != 0xFF {
		return 0, io.ErrUnexpectedEOF
	}
	// Füllbytes 0xFF überspringen
	for b == 0xFF {
		if b, err = br.ReadByte(); err != nil {
			return 0, err
		}
	}
	return b, nil
}

func tiffOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(tiff[2:]) != 42 {
		return 1
	}
	ifd := int(order.Uint32(tiff[4:]))
	if ifd < 8 || ifd+2 > len(tiff) {
		return 1
	}
	count := int(order.Uint16(tiff[ifd:]))
	for i := range count {
		entry := ifd + 2 + i*12
		if entry+12 > len(tiff) {
			return 1
		}
		if order.Uint16(tiff[entry:]) != orientationTag {
			continue
		}
		// Typ SHORT, der Wert steht direkt im Eintrag
		if v := int(order.Uint16(tiff[entry+8:])); v >= 1 && v <= 8 {
			return v
		}
		return 1
	}
	return 1
}

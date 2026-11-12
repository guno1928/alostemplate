package core

import (
	"strings"
	"unsafe"
)

const (
	narrowBlock      = 16
	wideBlock        = 32
	stringHeaderSize = unsafe.Sizeof("")
)

var literalPadding = strings.Repeat(" ", wideBlock)

func padSource(src string) string {
	return (src + literalPadding)[:len(src)]
}

func copyExact(to unsafe.Pointer, from unsafe.Pointer, n uintptr) {
	switch {
	case n > wideBlock:
		copy(unsafe.Slice((*byte)(to), n), unsafe.Slice((*byte)(from), n))
	case n > narrowBlock:
		*(*[16]byte)(to) = *(*[16]byte)(from)
		*(*[16]byte)(unsafe.Add(to, n-16)) = *(*[16]byte)(unsafe.Add(from, n-16))
	case n >= 8:
		*(*[8]byte)(to) = *(*[8]byte)(from)
		*(*[8]byte)(unsafe.Add(to, n-8)) = *(*[8]byte)(unsafe.Add(from, n-8))
	case n >= 4:
		*(*[4]byte)(to) = *(*[4]byte)(from)
		*(*[4]byte)(unsafe.Add(to, n-4)) = *(*[4]byte)(unsafe.Add(from, n-4))
	case n != 0:
		*(*byte)(to) = *(*byte)(from)
		*(*byte)(unsafe.Add(to, n>>1)) = *(*byte)(unsafe.Add(from, n>>1))
		*(*byte)(unsafe.Add(to, n-1)) = *(*byte)(unsafe.Add(from, n-1))
	}
}

const renderIncomplete = ^uintptr(0)

func (tpl *Template) wellFormed() bool {
	return len(tpl.literals) == len(tpl.slots)+1
}

func (tpl *Template) writeLinear(base unsafe.Pointer, pos uintptr, end uintptr, values unsafe.Pointer, stride uintptr) uintptr {
	if !tpl.wellFormed() {
		return pos
	}
	literals := unsafe.Pointer(unsafe.SliceData(tpl.literals))
	last := uintptr(len(tpl.slots))
	for i := uintptr(0); ; i++ {
		literal := (*gatherSeg)(unsafe.Add(literals, i*stringHeaderSize))
		n := literal.n
		if n <= narrowBlock && pos+narrowBlock <= end {
			*(*[16]byte)(unsafe.Add(base, pos)) = *(*[16]byte)(literal.ptr)
		} else if n <= wideBlock && pos+wideBlock <= end {
			*(*[16]byte)(unsafe.Add(base, pos)) = *(*[16]byte)(literal.ptr)
			*(*[16]byte)(unsafe.Add(base, pos+16)) = *(*[16]byte)(unsafe.Add(literal.ptr, 16))
		} else if n != 0 {
			if n > end-pos {
				return renderIncomplete
			}
			copyExact(unsafe.Add(base, pos), literal.ptr, n)
		}
		pos += n
		if i == last {
			if pos != end {
				return renderIncomplete
			}
			return pos
		}
		value := (*gatherSeg)(unsafe.Add(values, i*stride))
		n = value.n
		if n > end-pos {
			return renderIncomplete
		}
		if n != 0 {
			copyExact(unsafe.Add(base, pos), value.ptr, n)
		}
		pos += n
	}
}

func (tpl *Template) renderedSize(values unsafe.Pointer, stride uintptr) int {
	total := tpl.staticLen
	for i := range tpl.slots {
		total += int((*gatherSeg)(unsafe.Add(values, uintptr(i)*stride)).n)
	}
	return total
}

func (tpl *Template) writeFitting(dst []byte, values unsafe.Pointer, stride uintptr) uintptr {
	pos := uintptr(len(dst))
	if !tpl.wellFormed() {
		return pos
	}
	end := pos + uintptr(tpl.renderedSize(values, stride))
	if end > uintptr(cap(dst)) {
		return renderIncomplete
	}
	return tpl.writeLinear(unsafe.Pointer(unsafe.SliceData(dst)), pos, end, values, stride)
}

func (tpl *Template) appendLinear(dst []byte, values unsafe.Pointer, stride uintptr) []byte {
	if len(dst) != cap(dst) {
		end := tpl.renderFitting(dst, values, stride)
		if end != renderIncomplete {
			return dst[:end]
		}
	}
	return tpl.appendGrowing(dst, values, stride)
}

func (tpl *Template) appendGrowing(dst []byte, values unsafe.Pointer, stride uintptr) []byte {
	total := tpl.renderedSize(values, stride)
	start := len(dst)
	if total > cap(dst)-start {
		dst = growOutput(dst, total)
	}
	end := tpl.renderLinear(unsafe.Pointer(unsafe.SliceData(dst)), uintptr(start), uintptr(start+total), values, stride)
	if end == renderIncomplete {
		clear(dst[start : start+total])
		return tpl.appendSnapshot(dst, values, stride)
	}
	return dst[:end]
}

func growOutput(dst []byte, total int) []byte {
	used := len(dst)
	grown := makeUninitializedBytes(max(used+total, 2*cap(dst)))
	copy(grown, dst)
	clear(grown[used+total:])
	return grown[:used]
}

func (tpl *Template) appendSnapshot(dst []byte, values unsafe.Pointer, stride uintptr) []byte {
	snapshot := make([]string, len(tpl.slots))
	for i := range snapshot {
		snapshot[i] = *(*string)(unsafe.Add(values, uintptr(i)*stride))
	}
	return tpl.appendGrowing(dst, unsafe.Pointer(unsafe.SliceData(snapshot)), stringHeaderSize)
}

func (tpl *Template) fillSlotsFromStream(slotValues []string, values unsafe.Pointer, stride uintptr, count int) {
	slots := tpl.slots
	for i := range slotValues {
		slot := &slots[i]
		if slot.keyIndex < count {
			slotValues[i] = *(*string)(unsafe.Add(values, uintptr(slot.keyIndex)*stride))
		} else {
			slotValues[i] = slot.placeholder
		}
	}
}

func (tpl *Template) fillSlotsFromMap(slotValues []string, values map[string]string) {
	slots := tpl.slots
	keys := tpl.keys
	for i := range slotValues {
		slot := &slots[i]
		value, ok := values[keys[slot.keyIndex]]
		if !ok {
			value = slot.placeholder
		}
		slotValues[i] = value
	}
}

func (tpl *Template) appendExpanded(dst []byte, values unsafe.Pointer, stride uintptr, count int) []byte {
	slots := len(tpl.slots)
	if slots <= inlineKeyLimit {
		var inline [inlineKeyLimit]string
		tpl.fillSlotsFromStream(inline[:slots], values, stride, count)
		return tpl.appendLinear(dst, unsafe.Pointer(&inline), stringHeaderSize)
	}
	pooled := acquireRenderScratch(0, slots)
	tpl.fillSlotsFromStream(pooled.parts[:slots], values, stride, count)
	dst = tpl.appendLinear(dst, unsafe.Pointer(unsafe.SliceData(pooled.parts)), stringHeaderSize)
	releaseRenderScratch(pooled, 0)
	return dst
}

func appendMap(tpl *Template, dst []byte, values map[string]string) []byte {
	slots := len(tpl.slots)
	if slots <= inlineKeyLimit {
		var inline [inlineKeyLimit]string
		tpl.fillSlotsFromMap(inline[:slots], values)
		return tpl.appendLinear(dst, unsafe.Pointer(&inline), stringHeaderSize)
	}
	pooled := acquireRenderScratch(0, slots)
	tpl.fillSlotsFromMap(pooled.parts[:slots], values)
	dst = tpl.appendLinear(dst, unsafe.Pointer(unsafe.SliceData(pooled.parts)), stringHeaderSize)
	releaseRenderScratch(pooled, 0)
	return dst
}

func appendValues(tpl *Template, dst []byte, values []string) []byte {
	stream := unsafe.Pointer(unsafe.SliceData(values))
	if tpl.linear && len(values) >= len(tpl.keys) {
		return tpl.appendLinear(dst, stream, stringHeaderSize)
	}
	return tpl.appendExpanded(dst, stream, stringHeaderSize, len(values))
}

func (tpl *Template) pairsFollowKeyOrder(pairs []string) bool {
	keys := tpl.keys
	words := tpl.keyWords
	if len(keys) == 0 || len(words) != len(keys) || len(pairs) != 2*len(keys) {
		return false
	}
	for i, key := range keys {
		candidate := pairs[2*i]
		length := len(candidate)
		if length != len(key) {
			return false
		}
		if length > shortKeyLimit {
			if candidate != key {
				return false
			}
		} else if packShortKey(unsafe.Pointer(unsafe.StringData(candidate)), length) != words[i] {
			return false
		}
	}
	return true
}

func appendPairs(tpl *Template, dst []byte, pairs []string) []byte {
	keyCount := len(tpl.keys)
	if keyCount == 0 {
		return tpl.appendLinear(dst, nil, 0)
	}
	if len(pairs) == 2*keyCount && tpl.pairsFollowKeyOrder(pairs) {
		values := unsafe.Pointer(&pairs[1])
		if tpl.linear {
			return tpl.appendLinear(dst, values, 2*stringHeaderSize)
		}
		return tpl.appendExpanded(dst, values, 2*stringHeaderSize, keyCount)
	}
	if tpl.single.enabled {
		if len(pairs) == 1 {
			return tpl.appendLinear(dst, unsafe.Pointer(unsafe.SliceData(pairs)), stringHeaderSize)
		}
		return appendRendered(dst, tpl.replaceSingle(dst[len(dst):], pairs))
	}
	return appendRendered(dst, tpl.renderPairsGeneric(dst[len(dst):], pairs))
}

//go:build amd64 && !purego

#include "textflag.h"

// func gatherAsm(dst unsafe.Pointer, segs unsafe.Pointer, n int)
//
// Concatenates n segments into dst. Each segment is a 16-byte {ptr, len} pair.
// Copies are length-classed with overlapping loads/stores so short template
// literals avoid the startup cost of REP MOVSB.
TEXT ·gatherAsm(SB), NOSPLIT, $0-24
	MOVQ  dst+0(FP), DI
	MOVQ  segs+8(FP), SI
	MOVQ  n+16(FP), R8
	TESTQ R8, R8
	JZ    done

segloop:
	MOVQ  (SI), R9
	MOVQ  8(SI), R10
	ADDQ  $16, SI
	TESTQ R10, R10
	JZ    nextseg

	CMPQ R10, $8
	JB   lt8
	CMPQ R10, $16
	JBE  le16
	CMPQ R10, $32
	JBE  le32
	CMPQ R10, $64
	JBE  le64
	JMP  big

lt8:
	CMPQ R10, $4
	JB   lt4
	MOVL (R9), AX
	MOVL -4(R9)(R10*1), BX
	MOVL AX, (DI)
	MOVL BX, -4(DI)(R10*1)
	JMP  advance

lt4:
	CMPQ R10, $2
	JB   one
	MOVW (R9), AX
	MOVW -2(R9)(R10*1), BX
	MOVW AX, (DI)
	MOVW BX, -2(DI)(R10*1)
	JMP  advance

one:
	MOVB (R9), AX
	MOVB AX, (DI)
	JMP  advance

le16:
	MOVQ (R9), AX
	MOVQ -8(R9)(R10*1), BX
	MOVQ AX, (DI)
	MOVQ BX, -8(DI)(R10*1)
	JMP  advance

le32:
	MOVOU (R9), X0
	MOVOU -16(R9)(R10*1), X1
	MOVOU X0, (DI)
	MOVOU X1, -16(DI)(R10*1)
	JMP   advance

le64:
	MOVOU (R9), X0
	MOVOU 16(R9), X1
	MOVOU -32(R9)(R10*1), X2
	MOVOU -16(R9)(R10*1), X3
	MOVOU X0, (DI)
	MOVOU X1, 16(DI)
	MOVOU X2, -32(DI)(R10*1)
	MOVOU X3, -16(DI)(R10*1)
	JMP   advance

big:
	// Segments above 4096 bytes go to REP MOVSB, which wins once the string is
	// long enough to amortise its startup cost. R14/R15 are reserved by the Go
	// register ABI, so the goroutine pointer is never touched here.
	CMPQ R10, $4096
	JAE  huge

	// 64 bytes per iteration, then one overlapping 64-byte tail. Safe because
	// this path only runs when the segment is longer than 64 bytes.
	MOVQ R10, R11
	SHRQ $6, R11
	MOVQ R9, R12
	MOVQ DI, R13

blk64:
	MOVOU (R12), X0
	MOVOU 16(R12), X1
	MOVOU 32(R12), X2
	MOVOU 48(R12), X3
	MOVOU X0, (R13)
	MOVOU X1, 16(R13)
	MOVOU X2, 32(R13)
	MOVOU X3, 48(R13)
	ADDQ  $64, R12
	ADDQ  $64, R13
	DECQ  R11
	JNZ   blk64

	MOVOU -64(R9)(R10*1), X0
	MOVOU -48(R9)(R10*1), X1
	MOVOU -32(R9)(R10*1), X2
	MOVOU -16(R9)(R10*1), X3
	MOVOU X0, -64(DI)(R10*1)
	MOVOU X1, -48(DI)(R10*1)
	MOVOU X2, -32(DI)(R10*1)
	MOVOU X3, -16(DI)(R10*1)
	JMP   advance

huge:
	MOVQ DX, R11
	MOVQ SI, DX
	MOVQ R9, SI
	MOVQ R10, CX
	REP; MOVSB
	MOVQ DX, SI
	MOVQ R11, DX
	JMP  nextseg

advance:
	ADDQ R10, DI

nextseg:
	DECQ R8
	JNZ  segloop

done:
	RET

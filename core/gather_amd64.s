//go:build amd64 && !purego

#include "textflag.h"

// func gatherAsm(dst unsafe.Pointer, segs unsafe.Pointer, n int)
//
// Concatenates n segments into dst. Each segment is a 16-byte {ptr, len} pair.
// Copies are length-classed with overlapping loads/stores so short template
// literals avoid the startup cost of REP MOVSB.
TEXT ·gatherAsm(SB), NOSPLIT, $0-24
	MOVQ dst+0(FP), DI
	MOVQ segs+8(FP), SI
	MOVQ n+16(FP), R8
	LEAQ 16(SI), DX
	MOVQ $32, R11
	MOVQ $32, R13
	MOVQ $-1, R12
	CALL gatherStreams<>(SB)
	RET

TEXT ·gatherStreamsAsm(SB), NOSPLIT, $0-64
	MOVQ dst+0(FP), DI
	MOVQ room+8(FP), R12
	ADDQ DI, R12
	MOVQ first+16(FP), SI
	MOVQ firstStride+24(FP), R11
	MOVQ second+32(FP), DX
	MOVQ secondStride+40(FP), R13
	MOVQ n+48(FP), R8
	CALL gatherStreams<>(SB)
	SUBQ dst+0(FP), DI
	MOVQ DI, ret+56(FP)
	RET

TEXT gatherStreams<>(SB), NOSPLIT|NOFRAME, $0-0
	TESTQ R8, R8
	JZ    done

segloop:
	MOVQ  (SI), R9
	MOVQ  8(SI), R10
	ADDQ  R11, SI
	XCHGQ SI, DX
	XCHGQ R11, R13
	TESTQ R10, R10
	JZ    nextseg
	LEAQ  (DI)(R10*1), AX
	CMPQ  AX, R12
	JA    done

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
	CMPQ R10, $128
	JB   blocks64
	CMPB ·gatherUseAVX2(SB), $0
	JNE  blocks128

	// Segments above 4096 bytes go to REP MOVSB, which wins once the string is
	// long enough to amortise its startup cost. R14/R15 are reserved by the Go
	// register ABI, so the goroutine pointer is never touched here.
	CMPQ R10, $4096
	JAE  huge

blocks64:
	// 64 bytes per iteration, then one overlapping 64-byte tail. Safe because
	// this path only runs when the segment is longer than 64 bytes.
	XORQ CX, CX
	LEAQ -64(R10), AX

blk64:
	MOVOU (R9)(CX*1), X0
	MOVOU 16(R9)(CX*1), X1
	MOVOU 32(R9)(CX*1), X2
	MOVOU 48(R9)(CX*1), X3
	MOVOU X0, (DI)(CX*1)
	MOVOU X1, 16(DI)(CX*1)
	MOVOU X2, 32(DI)(CX*1)
	MOVOU X3, 48(DI)(CX*1)
	ADDQ  $64, CX
	CMPQ  CX, AX
	JB    blk64

	MOVOU (R9)(AX*1), X0
	MOVOU 16(R9)(AX*1), X1
	MOVOU 32(R9)(AX*1), X2
	MOVOU 48(R9)(AX*1), X3
	MOVOU X0, (DI)(AX*1)
	MOVOU X1, 16(DI)(AX*1)
	MOVOU X2, 32(DI)(AX*1)
	MOVOU X3, 48(DI)(AX*1)
	JMP   advance

blocks128:
	XORQ  CX, CX
	LEAQ  -128(R10), AX
	TESTQ AX, AX
	JZ    tail128

blk128:
	VMOVDQU (R9)(CX*1), Y0
	VMOVDQU 32(R9)(CX*1), Y1
	VMOVDQU 64(R9)(CX*1), Y2
	VMOVDQU 96(R9)(CX*1), Y3
	VMOVDQU Y0, (DI)(CX*1)
	VMOVDQU Y1, 32(DI)(CX*1)
	VMOVDQU Y2, 64(DI)(CX*1)
	VMOVDQU Y3, 96(DI)(CX*1)
	ADDQ    $128, CX
	CMPQ    CX, AX
	JB      blk128

tail128:
	VMOVDQU (R9)(AX*1), Y0
	VMOVDQU 32(R9)(AX*1), Y1
	VMOVDQU 64(R9)(AX*1), Y2
	VMOVDQU 96(R9)(AX*1), Y3
	VMOVDQU Y0, (DI)(AX*1)
	VMOVDQU Y1, 32(DI)(AX*1)
	VMOVDQU Y2, 64(DI)(AX*1)
	VMOVDQU Y3, 96(DI)(AX*1)
	VZEROUPPER
	JMP     advance

huge:
	MOVQ SI, AX
	MOVQ R9, SI
	MOVQ R10, CX
	REP; MOVSB
	MOVQ AX, SI
	JMP  nextseg

advance:
	ADDQ R10, DI

nextseg:
	DECQ R8
	JNZ  segloop

done:
	RET

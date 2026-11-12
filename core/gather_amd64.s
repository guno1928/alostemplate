//go:build amd64 && !purego

#include "go_asm.h"
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
	JZ    gatherdone

gatherloop:
	MOVQ  (SI), R9
	MOVQ  8(SI), R10
	ADDQ  $16, SI
	TESTQ R10, R10
	JZ    gathernext
	CALL  copySegment<>(SB)

gathernext:
	DECQ R8
	JNZ  gatherloop

gatherdone:
	RET

TEXT ·renderLinearAsm(SB), NOSPLIT, $0-56
	PCALIGN $64
	MOVQ    tpl+24(FP), BX
	MOVQ    pos+8(FP), AX
	MOVQ    Template_slots+8(BX), R8
	LEAQ    1(R8), CX
	CMPQ    CX, Template_literals+8(BX)
	JNE     malformed
	MOVQ    base+0(FP), DI
	MOVQ    end+16(FP), R12
	MOVQ    Template_literals(BX), SI
	MOVQ    values+32(FP), DX
	MOVQ    stride+40(FP), R13
	ADDQ    DI, R12
	ADDQ    AX, DI
	JMP     renderSegments<>(SB)

malformed:
	MOVQ AX, ret+48(FP)
	RET

TEXT ·renderFittingAsm(SB), NOSPLIT, $0-56
	PCALIGN $64
	MOVQ    tpl+24(FP), BX
	MOVQ    pos+8(FP), AX
	MOVQ    Template_slots+8(BX), R8
	LEAQ    1(R8), CX
	CMPQ    CX, Template_literals+8(BX)
	JNE     malformed
	MOVQ    values+32(FP), DX
	MOVQ    stride+40(FP), R13
	MOVQ    Template_staticLen(BX), R12
	ADDQ    AX, R12
	MOVQ    DX, R9
	MOVQ    R8, CX
	XORQ    R11, R11
	SUBQ    $4, CX
	JB      sizerest
	LEAQ    (R13)(R13*2), R10
	PCALIGN $32

sizefour:
	ADDQ 8(R9), R12
	ADDQ 8(R9)(R13*1), R11
	ADDQ 8(R9)(R13*2), R12
	ADDQ 8(R9)(R10*1), R11
	LEAQ (R9)(R13*4), R9
	SUBQ $4, CX
	JAE  sizefour

sizerest:
	ADDQ    $4, CX
	JZ      sized
	PCALIGN $16

sizeone:
	ADDQ 8(R9), R12
	ADDQ R13, R9
	DECQ CX
	JNZ  sizeone

sized:
	ADDQ R11, R12
	CMPQ R12, limit+16(FP)
	JA   toosmall
	MOVQ base+0(FP), DI
	MOVQ Template_literals(BX), SI
	ADDQ DI, R12
	ADDQ AX, DI
	JMP  renderSegments<>(SB)

malformed:
	MOVQ AX, ret+48(FP)
	RET

toosmall:
	MOVQ $-1, ret+48(FP)
	RET

TEXT renderSegments<>(SB), NOSPLIT, $0-56
	PCALIGN $64
	LEAQ    -16(R12), R11
	LEAQ    -32(R12), BX

literal:
	MOVQ  (SI), R9
	MOVQ  8(SI), R10
	ADDQ  $16, SI
	CMPQ  R10, $16
	JA    literalwide
	CMPQ  DI, R11
	JG    literalexact
	MOVOU (R9), X0
	MOVOU X0, (DI)
	ADDQ  R10, DI

value:
	SUBQ $1, R8
	JB   finished
	MOVQ (DX), R9
	MOVQ 8(DX), R10
	ADDQ R13, DX
	MOVQ R12, AX
	SUBQ DI, AX
	CMPQ R10, AX
	JA   interrupted
	CMPQ R10, $16
	JA   valuelong
	CMPQ R10, $8
	JB   valueshort
	MOVQ (R9), AX
	MOVQ -8(R9)(R10*1), CX
	MOVQ AX, (DI)
	MOVQ CX, -8(DI)(R10*1)
	ADDQ R10, DI
	JMP  literal

literalwide:
	CMPQ  R10, $32
	JA    literalmid
	CMPQ  DI, BX
	JG    literalexact
	MOVOU (R9), X0
	MOVOU 16(R9), X1
	MOVOU X0, (DI)
	MOVOU X1, 16(DI)
	ADDQ  R10, DI
	JMP   value

literalmid:
	MOVQ  R12, AX
	SUBQ  DI, AX
	CMPQ  R10, AX
	JA    interrupted
	CMPQ  R10, $64
	JA    literalcall
	MOVOU (R9), X0
	MOVOU 16(R9), X1
	MOVOU -32(R9)(R10*1), X2
	MOVOU -16(R9)(R10*1), X3
	MOVOU X0, (DI)
	MOVOU X1, 16(DI)
	MOVOU X2, -32(DI)(R10*1)
	MOVOU X3, -16(DI)(R10*1)
	ADDQ  R10, DI
	JMP   value

literalexact:
	MOVQ  R12, AX
	SUBQ  DI, AX
	CMPQ  R10, AX
	JA    interrupted
	CMPQ  R10, $16
	JA    literaltail32
	CMPQ  R10, $8
	JB    literalsmall
	MOVQ  (R9), AX
	MOVQ  -8(R9)(R10*1), CX
	MOVQ  AX, (DI)
	MOVQ  CX, -8(DI)(R10*1)
	ADDQ  R10, DI
	JMP   value

literaltail32:
	MOVOU (R9), X0
	MOVOU -16(R9)(R10*1), X1
	MOVOU X0, (DI)
	MOVOU X1, -16(DI)(R10*1)
	ADDQ  R10, DI
	JMP   value

literalsmall:
	TESTQ R10, R10
	JZ    value

literalcall:
	CALL copySegment<>(SB)
	JMP  value

valueshort:
	CMPQ R10, $4
	JB   valuetiny
	MOVL (R9), AX
	MOVL -4(R9)(R10*1), CX
	MOVL AX, (DI)
	MOVL CX, -4(DI)(R10*1)
	ADDQ R10, DI
	JMP  literal

valuetiny:
	TESTQ   R10, R10
	JZ      literal
	MOVBLZX (R9), AX
	MOVB    AX, (DI)
	MOVQ    R10, CX
	SHRQ    $1, CX
	MOVBLZX (R9)(CX*1), AX
	MOVB    AX, (DI)(CX*1)
	MOVBLZX -1(R9)(R10*1), AX
	MOVB    AX, -1(DI)(R10*1)
	ADDQ    R10, DI
	JMP     literal

valuelong:
	CMPQ  R10, $32
	JA    value64
	MOVOU (R9), X0
	MOVOU -16(R9)(R10*1), X1
	MOVOU X0, (DI)
	MOVOU X1, -16(DI)(R10*1)
	ADDQ  R10, DI
	JMP   literal

value64:
	CMPQ  R10, $64
	JA    valuecall
	MOVOU (R9), X0
	MOVOU 16(R9), X1
	MOVOU -32(R9)(R10*1), X2
	MOVOU -16(R9)(R10*1), X3
	MOVOU X0, (DI)
	MOVOU X1, 16(DI)
	MOVOU X2, -32(DI)(R10*1)
	MOVOU X3, -16(DI)(R10*1)
	ADDQ  R10, DI
	JMP   literal

valuecall:
	CALL copySegment<>(SB)
	JMP  literal

finished:
	CMPQ DI, R12
	JNE  interrupted
	SUBQ base+0(FP), DI
	MOVQ DI, ret+48(FP)
	RET

interrupted:
	MOVQ $-1, ret+48(FP)
	RET

TEXT copySegment<>(SB), NOSPLIT|NOFRAME, $0-0
	PCALIGN $64
	CMPQ R10, $8
	JB   lt8
	CMPQ R10, $16
	JBE  le16
	CMPQ R10, $32
	JBE  le32
	CMPQ R10, $64
	JBE  le64
	CMPB ·gatherUseAVX2(SB), $0
	JNE  wide
	CMPQ R10, $128
	JB   blocks64

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
	ADDQ  R10, DI
	RET

wide:
	CMPQ    R10, $128
	JA      blocks128
	VMOVDQU (R9), Y0
	VMOVDQU 32(R9), Y1
	VMOVDQU -64(R9)(R10*1), Y2
	VMOVDQU -32(R9)(R10*1), Y3
	VMOVDQU Y0, (DI)
	VMOVDQU Y1, 32(DI)
	VMOVDQU Y2, -64(DI)(R10*1)
	VMOVDQU Y3, -32(DI)(R10*1)
	VZEROUPPER
	ADDQ    R10, DI
	RET

blocks128:
	XORQ CX, CX
	LEAQ -128(R10), AX

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
	ADDQ    R10, DI
	RET

huge:
	MOVQ SI, AX
	MOVQ R9, SI
	MOVQ R10, CX
	REP; MOVSB
	MOVQ AX, SI
	RET

lt8:
	CMPQ R10, $4
	JB   lt4
	MOVL (R9), AX
	MOVL -4(R9)(R10*1), CX
	MOVL AX, (DI)
	MOVL CX, -4(DI)(R10*1)
	ADDQ R10, DI
	RET

lt4:
	CMPQ R10, $2
	JB   one
	MOVW (R9), AX
	MOVW -2(R9)(R10*1), CX
	MOVW AX, (DI)
	MOVW CX, -2(DI)(R10*1)
	ADDQ R10, DI
	RET

one:
	MOVB (R9), AX
	MOVB AX, (DI)
	ADDQ R10, DI
	RET

le16:
	MOVQ (R9), AX
	MOVQ -8(R9)(R10*1), CX
	MOVQ AX, (DI)
	MOVQ CX, -8(DI)(R10*1)
	ADDQ R10, DI
	RET

le32:
	MOVOU (R9), X0
	MOVOU -16(R9)(R10*1), X1
	MOVOU X0, (DI)
	MOVOU X1, -16(DI)(R10*1)
	ADDQ  R10, DI
	RET

le64:
	MOVOU (R9), X0
	MOVOU 16(R9), X1
	MOVOU -32(R9)(R10*1), X2
	MOVOU -16(R9)(R10*1), X3
	MOVOU X0, (DI)
	MOVOU X1, 16(DI)
	MOVOU X2, -32(DI)(R10*1)
	MOVOU X3, -16(DI)(R10*1)
	ADDQ  R10, DI
	RET

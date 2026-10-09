//go:build darwin

// A trampoline to libSystem's proc_pidinfo, the same shape as those of
// package syscall and x/sys/unix; JMP assembles on amd64 and arm64 alike.

#include "textflag.h"

TEXT libc_proc_pidinfo_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_proc_pidinfo(SB)

GLOBL	·libcProcPidinfoTrampolineAddr(SB), RODATA, $8
DATA	·libcProcPidinfoTrampolineAddr(SB)/8, $libc_proc_pidinfo_trampoline<>(SB)

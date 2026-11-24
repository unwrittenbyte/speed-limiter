.section .rsrc$01,"dr"
.p2align 2
root:
.long 0,0
.short 0,0,0,1
.long 24,0x80000000+(type-root)
type:
.long 0,0
.short 0,0,0,1
.long 1,0x80000000+(name-root)
name:
.long 0,0
.short 0,0,0,1
.long 1033,(entry-root)
entry:
.long payload@IMGREL
.long payload_end-payload
.long 65001,0
.section .rsrc$02,"dr"
.p2align 2
payload:
.incbin "setup.manifest"
payload_end:

package pickle

// The opcodes of the pickle format, as Python's pickle.py names them.
const (
	opMark           = '('
	opStop           = '.'
	opPop            = '0'
	opPopMark        = '1'
	opDup            = '2'
	opFloat          = 'F'
	opInt            = 'I'
	opBinInt         = 'J'
	opBinInt1        = 'K'
	opLong           = 'L'
	opBinInt2        = 'M'
	opNone           = 'N'
	opPersID         = 'P'
	opBinPersID      = 'Q'
	opReduce         = 'R'
	opString         = 'S'
	opBinString      = 'T'
	opShortBinString = 'U'
	opUnicode        = 'V'
	opBinUnicode     = 'X'
	opAppend         = 'a'
	opBuild          = 'b'
	opGlobal         = 'c'
	opDict           = 'd'
	opEmptyDict      = '}'
	opAppends        = 'e'
	opGet            = 'g'
	opBinGet         = 'h'
	opInst           = 'i'
	opLongBinGet     = 'j'
	opList           = 'l'
	opEmptyList      = ']'
	opObj            = 'o'
	opPut            = 'p'
	opBinPut         = 'q'
	opLongBinPut     = 'r'
	opSetItem        = 's'
	opTuple          = 't'
	opEmptyTuple     = ')'
	opSetItems       = 'u'
	opBinFloat       = 'G'

	opProto  = 0x80
	opNewObj = 0x81
	opExt1   = 0x82
	opExt2   = 0x83
	opExt4   = 0x84
	opTuple1 = 0x85
	opTuple2 = 0x86
	opTuple3 = 0x87
	opTrue   = 0x88
	opFalse  = 0x89
	opLong1  = 0x8a
	opLong4  = 0x8b

	opBinBytes      = 'B'
	opShortBinBytes = 'C'

	opShortBinUnicode = 0x8c
	opBinUnicode8     = 0x8d
	opBinBytes8       = 0x8e
	opEmptySet        = 0x8f
	opAddItems        = 0x90
	opFrozenSet       = 0x91
	opNewObjEx        = 0x92
	opStackGlobal     = 0x93
	opMemoize         = 0x94
	opFrame           = 0x95

	opByteArray8     = 0x96
	opNextBuffer     = 0x97
	opReadonlyBuffer = 0x98
)

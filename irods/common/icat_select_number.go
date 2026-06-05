package common

// ICATSelectFunction is an ICAT Select function type
type ICATSelectFunction int

const (
	ICAT_SELECT_FUNC_NONE  ICATSelectFunction = 1
	ICAT_SELECT_FUNC_SUM   ICATSelectFunction = 4
	ICAT_SELECT_FUNC_COUNT ICATSelectFunction = 6
)

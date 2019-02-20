package gun

import "strconv"

var SoulGenDefault = func() Soul {
	ms, uniqueNum := TimeNowUniqueUnix()
	s := strconv.FormatInt(ms, 36)
	if uniqueNum > 0 {
		s += strconv.FormatInt(uniqueNum, 36)
	}
	return Soul(s + randString(12))
}

type Node struct {
	NodeMetadata
	Values map[string]NodeValue
}

type NodeMetadata struct {
	Soul     Soul
	HAMState map[string]uint64
}

type Soul string

type NodeValue interface {
}

type NodeString string
type NodeNumber string
type NodeBool bool
type NodeRelation Soul

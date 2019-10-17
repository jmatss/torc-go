package logger

import "log"

type Level int

var (
	CurrentLevel = Low
)

const (
	None Level = iota
	Low
	High
)

func (l Level) String() string {
	return GetValues()[l]
}

func GetValues() []string {
	return []string{
		"None",
		"Low",
		"High",
	}
}

func Log(level Level, format string, v ...interface{}) {
	if CurrentLevel >= level {
		log.Printf(format, v)
	}
}

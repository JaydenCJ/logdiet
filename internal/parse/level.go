package parse

import "strings"

// Level is a normalized log severity. The numeric order matters: a
// statement is demotable when its level sorts strictly below the level the
// user wants to keep in production, so Unknown deliberately sorts below
// Trace and is never demotable (we refuse to suggest cutting lines we
// could not classify).
type Level int

const (
	Unknown Level = iota
	Trace
	Debug
	Info
	Warn
	Error
	Fatal
)

// String returns the canonical lowercase name used in every output format.
func (l Level) String() string {
	switch l {
	case Trace:
		return "trace"
	case Debug:
		return "debug"
	case Info:
		return "info"
	case Warn:
		return "warn"
	case Error:
		return "error"
	case Fatal:
		return "fatal"
	default:
		return "unknown"
	}
}

// levelNames maps every spelling we accept to a normalized Level. Covers
// the common Go/Java/Python/Rust loggers plus syslog-ish aliases.
var levelNames = map[string]Level{
	"trace":         Trace,
	"trc":           Trace,
	"verbose":       Trace,
	"finest":        Trace,
	"debug":         Debug,
	"dbg":           Debug,
	"fine":          Debug,
	"finer":         Debug,
	"info":          Info,
	"information":   Info,
	"informational": Info,
	"notice":        Info,
	"warn":          Warn,
	"warning":       Warn,
	"wrn":           Warn,
	"error":         Error,
	"err":           Error,
	"severe":        Error,
	"fatal":         Fatal,
	"crit":          Fatal,
	"critical":      Fatal,
	"panic":         Fatal,
	"alert":         Fatal,
	"emerg":         Fatal,
}

// numericLevels maps the pino/bunyan numeric convention (10..60) to
// normalized levels; JSON loggers in the Node ecosystem emit these.
var numericLevels = map[string]Level{
	"10": Trace,
	"20": Debug,
	"30": Info,
	"40": Warn,
	"50": Error,
	"60": Fatal,
}

// ParseLevel normalizes a level token from any supported spelling,
// tolerating brackets, trailing colons, and any letter case.
// It returns Unknown for anything unrecognized.
func ParseLevel(s string) Level {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "[]():")
	if l, ok := numericLevels[s]; ok {
		return l
	}
	if l, ok := levelNames[strings.ToLower(s)]; ok {
		return l
	}
	return Unknown
}

// ParseKeepLevel parses a user-supplied --keep value. Unlike ParseLevel it
// rejects unknown spellings instead of defaulting, because a typo here
// would silently change which statements the plan is allowed to demote.
func ParseKeepLevel(s string) (Level, bool) {
	l := ParseLevel(s)
	return l, l != Unknown
}

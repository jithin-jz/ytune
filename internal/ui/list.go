package ui

// list is a scrolling list of rows with a highlighted one.
type list struct {
	rows   []string
	cursor int
	offset int // The first visible row.
}

func (l *list) setRows(rows []string, height int) {
	l.rows = rows
	l.clamp(height)
}

// clamp keeps the cursor on a real row and that row on screen.
func (l *list) clamp(height int) {
	l.cursor = max(min(l.cursor, len(l.rows)-1), 0)
	if l.cursor < l.offset {
		l.offset = l.cursor
	}
	if l.cursor >= l.offset+height {
		l.offset = l.cursor - height + 1
	}
	l.offset = max(min(l.offset, len(l.rows)-height), 0)
}

// navigate handles the movement keys, reporting whether key was one of them.
func (l *list) navigate(key string, height int) bool {
	switch key {
	case "up":
		l.cursor--
	case "down":
		l.cursor++
	case "pgup":
		l.cursor -= height
	case "pgdown":
		l.cursor += height
	case "home":
		l.cursor = 0
	case "end":
		l.cursor = len(l.rows) - 1
	default:
		return false
	}
	l.clamp(height)
	return true
}

func (l *list) render(width, height int, focused bool) []string {
	lines := make([]string, 0, height)
	for i := l.offset; i < len(l.rows) && len(lines) < height; i++ {
		line := fit(l.rows[i], width)
		switch {
		case i != l.cursor:
		case focused:
			line = styleRow.Render(line)
		default:
			line = styleRowDim.Render(line)
		}
		lines = append(lines, line)
	}
	return lines
}

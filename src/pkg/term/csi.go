package term

import "strconv"

// csi acts on one control sequence: the parameters as written, and the letter that ends it.
func (s *Screen) csi(params string, final byte) {
	p := parseParams(params)

	// A parameter that is absent or zero means one, for every sequence that counts something.
	at := func(i, fallback int) int {
		if i >= len(p) || p[i] == 0 {
			return fallback
		}
		return p[i]
	}

	switch final {
	case 'A':
		s.y = max(0, s.y-at(0, 1))
	case 'B':
		s.y = min(s.rows-1, s.y+at(0, 1))
	case 'C':
		s.x = min(s.cols-1, s.x+at(0, 1))
	case 'D':
		s.x = max(0, s.x-at(0, 1))
	case 'E':
		s.x = 0
		s.y = min(s.rows-1, s.y+at(0, 1))
	case 'F':
		s.x = 0
		s.y = max(0, s.y-at(0, 1))
	case 'G':
		s.x = min(s.cols-1, at(0, 1)-1)
	case 'd':
		s.y = min(s.rows-1, at(0, 1)-1)
	case 'H', 'f':
		s.y = min(s.rows-1, at(0, 1)-1)
		s.x = min(s.cols-1, at(1, 1)-1)
	case 'J':
		s.eraseDisplay(first(p))
	case 'K':
		s.eraseLine(first(p))
	case 'L':
		s.insertLines(at(0, 1))
	case 'M':
		s.deleteLines(at(0, 1))
	case 'P':
		s.deleteChars(at(0, 1))
	case '@':
		s.insertChars(at(0, 1))
	case 'X':
		s.eraseChars(at(0, 1))
	case 's':
		s.save()
	case 'u':
		s.restore()
	case 'm':
		s.sgr(p)
	}
}

func first(p []int) int {
	if len(p) == 0 {
		return 0
	}
	return p[0]
}

// parseParams splits "1;32" into its numbers. A private marker such as "?25" is dropped: nothing
// here has a mode to set.
func parseParams(text string) []int {
	if text != "" {
		switch text[0] {
		case '?', '<', '>', '!':
			text = text[1:]
		}
	}
	if text == "" {
		return nil
	}

	out := make([]int, 0, 4)
	start := 0
	for i := 0; i <= len(text); i++ {
		if i == len(text) || text[i] == ';' {
			n, _ := strconv.Atoi(text[start:i])
			out = append(out, n)
			start = i + 1
		}
	}
	return out
}

func (s *Screen) eraseDisplay(mode int) {
	switch mode {
	case 2, 3:
		s.Clear()
	case 0:
		s.eraseLine(0)
		for y := s.y + 1; y < s.rows; y++ {
			s.grid[y] = blankRow(s.cols)
		}
	case 1:
		s.eraseLine(1)
		for y := 0; y < s.y; y++ {
			s.grid[y] = blankRow(s.cols)
		}
	}
}

func (s *Screen) eraseLine(mode int) {
	row := s.grid[s.y]

	from, to := 0, s.cols
	switch mode {
	case 0:
		from = s.x
	case 1:
		to = min(s.x+1, s.cols)
	}
	for i := from; i < to; i++ {
		row[i] = blank()
	}
}

func (s *Screen) eraseChars(count int) {
	row := s.grid[s.y]
	for i := s.x; i < min(s.cols, s.x+count); i++ {
		row[i] = blank()
	}
}

func (s *Screen) insertLines(count int) {
	for range count {
		copy(s.grid[s.y+1:], s.grid[s.y:])
		s.grid[s.y] = blankRow(s.cols)
	}
}

func (s *Screen) deleteLines(count int) {
	for range count {
		copy(s.grid[s.y:], s.grid[s.y+1:])
		s.grid[s.rows-1] = blankRow(s.cols)
	}
}

func (s *Screen) insertChars(count int) {
	row := s.grid[s.y]
	for range count {
		copy(row[s.x+1:], row[s.x:])
		row[s.x] = blank()
	}
}

func (s *Screen) deleteChars(count int) {
	row := s.grid[s.y]
	for range count {
		copy(row[s.x:], row[s.x+1:])
		row[s.cols-1] = blank()
	}
}

// sgr sets how the characters after it are drawn.
func (s *Screen) sgr(p []int) {
	if len(p) == 0 {
		s.cur = Style{}
		return
	}

	for i := 0; i < len(p); i++ {
		switch n := p[i]; {
		case n == 0:
			s.cur = Style{}
		case n == 1:
			s.cur.Bold = true
		case n == 2:
			s.cur.Dim = true
		case n == 3:
			s.cur.Italic = true
		case n == 4:
			s.cur.Underline = true
		case n == 7:
			s.cur.Inverse = true
		case n == 22:
			s.cur.Bold, s.cur.Dim = false, false
		case n == 23:
			s.cur.Italic = false
		case n == 24:
			s.cur.Underline = false
		case n == 27:
			s.cur.Inverse = false
		case n >= 30 && n <= 37:
			s.cur.FG = Indexed(uint8(n - 30))
		case n >= 90 && n <= 97:
			s.cur.FG = Indexed(uint8(n - 90 + 8))
		case n >= 40 && n <= 47:
			s.cur.BG = Indexed(uint8(n - 40))
		case n >= 100 && n <= 107:
			s.cur.BG = Indexed(uint8(n - 100 + 8))
		case n == 39:
			s.cur.FG = Color{}
		case n == 49:
			s.cur.BG = Color{}
		case n == 38 || n == 48:
			// Both extended forms carry their arguments in the same list.
			used, c, ok := extended(p[i:])
			if ok {
				if n == 38 {
					s.cur.FG = c
				} else {
					s.cur.BG = c
				}
			}
			i += used
		}
	}
}

// extended reads a 256-colour or true-colour argument, reporting how many extra parameters it ate.
func extended(p []int) (int, Color, bool) {
	if len(p) < 2 {
		return 0, Color{}, false
	}

	switch p[1] {
	case 5:
		if len(p) < 3 {
			return len(p) - 1, Color{}, false
		}
		return 2, Indexed(uint8(p[2])), true
	case 2:
		if len(p) < 5 {
			return len(p) - 1, Color{}, false
		}
		return 4, RGB(uint8(p[2]), uint8(p[3]), uint8(p[4])), true
	}
	return 1, Color{}, false
}

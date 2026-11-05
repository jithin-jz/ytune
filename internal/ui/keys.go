package ui

// The keyboard shortcuts. Change a value here to rebind that action.
const (
	keySearch      = "/"
	keyPause       = "space"
	keyEnqueue     = "a"
	keyRemove      = "d"
	keyNext        = "n"
	keyPrevious    = "b"
	keySeekBack    = "left"
	keySeekForward = "right"
	keyVolumeDown  = "-"
	keyVolumeUp    = "+"
	keyVolumeUpAlt = "=" // The same physical key as + without Shift.
	keyQuit        = "q"
)

const (
	seekSeconds = 5
	volumeStep  = 5
)

// helpItems are the shortcuts listed along the bottom of the screen. They're
// kept short enough to fit a standard 80-column window.
var helpItems = []struct{ key, label string }{
	{keySearch, "search"},
	{keyPause, "pause"},
	{keyEnqueue, "add"},
	{keyRemove, "remove"},
	{keyNext, "next"},
	{keyPrevious, "back"},
	{keyVolumeDown + " " + keyVolumeUp, "volume"},
	{keyQuit, "quit"},
}

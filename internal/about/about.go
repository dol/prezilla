// Package about holds the program's identity shown by `prezilla about` and
// the tray's About entry.
package about

// Identity constants. Website is an assumed URL for the project repository.
const (
	Name         = "Prezilla"
	Comments     = "Presentation mode switcher"
	License      = "Apache-2.0"
	Website      = "https://github.com/dol/prezilla"
	WebsiteLabel = "github.com/dol/prezilla"
	IconName     = "prezilla"
)

// Info describes the program for an About dialog or text output.
type Info struct {
	Name, Version, Comments, License, Website, WebsiteLabel, IconName string
}

// New returns the Info for version; an empty version becomes "dev".
func New(version string) Info {
	if version == "" {
		version = "dev"
	}
	return Info{Name, version, Comments, License, Website, WebsiteLabel, IconName}
}

// Text renders four newline-terminated lines.
func (i Info) Text() string {
	return i.Name + " " + i.Version + "\n" + i.Comments + "\nLicense: " + i.License + "\nWebsite: " + i.Website + "\n"
}

package theme

// Theme is deliberately data-only so the UI can persist a theme name without
// serializing terminal-library types.
type Theme struct {
	Name       string
	Background string
	Surface    string
	Panel      string
	Border     string
	Text       string
	Muted      string
	Primary    string
	Secondary  string
	Success    string
	Warning    string
	Danger     string
	Selection  string
}

var All = []Theme{
	{
		Name:       "Arrakis",
		Background: "#1B1612",
		Surface:    "#251D17",
		Panel:      "#30251C",
		Border:     "#65513D",
		Text:       "#E8D7B4",
		Muted:      "#9F8B70",
		Primary:    "#E5A853",
		Secondary:  "#88A875",
		Success:    "#7EBE84",
		Warning:    "#E5A853",
		Danger:     "#D46A5E",
		Selection:  "#493827",
	},
	{
		Name:       "Giedi Prime",
		Background: "#101218",
		Surface:    "#171A23",
		Panel:      "#202431",
		Border:     "#3C4254",
		Text:       "#D8DEE9",
		Muted:      "#78839A",
		Primary:    "#7AA2F7",
		Secondary:  "#BB9AF7",
		Success:    "#9ECE6A",
		Warning:    "#E0AF68",
		Danger:     "#F7768E",
		Selection:  "#2A3550",
	},
	{
		Name:       "Bene Gesserit",
		Background: "#1E1E2E",
		Surface:    "#181825",
		Panel:      "#313244",
		Border:     "#585B70",
		Text:       "#CDD6F4",
		Muted:      "#7F849C",
		Primary:    "#CBA6F7",
		Secondary:  "#89B4FA",
		Success:    "#A6E3A1",
		Warning:    "#F9E2AF",
		Danger:     "#F38BA8",
		Selection:  "#45475A",
	},
	{
		Name:       "Caladan",
		Background: "#2E3440",
		Surface:    "#3B4252",
		Panel:      "#434C5E",
		Border:     "#4C566A",
		Text:       "#ECEFF4",
		Muted:      "#A3B1C6",
		Primary:    "#88C0D0",
		Secondary:  "#B48EAD",
		Success:    "#A3BE8C",
		Warning:    "#EBCB8B",
		Danger:     "#BF616A",
		Selection:  "#4C566A",
	},
	{
		Name:       "Ix",
		Background: "#111111",
		Surface:    "#191919",
		Panel:      "#242424",
		Border:     "#555555",
		Text:       "#EEEEEE",
		Muted:      "#999999",
		Primary:    "#FFFFFF",
		Secondary:  "#BBBBBB",
		Success:    "#DDDDDD",
		Warning:    "#CCCCCC",
		Danger:     "#AAAAAA",
		Selection:  "#3A3A3A",
	},
}

func ByName(name string) (Theme, int) {
	legacyNames := map[string]string{
		"Dune": "Arrakis", "Midnight": "Giedi Prime", "Mauve": "Bene Gesserit",
		"Nord": "Caladan", "Mono": "Ix",
	}
	if migrated, ok := legacyNames[name]; ok {
		name = migrated
	}
	for i, candidate := range All {
		if candidate.Name == name {
			return candidate, i
		}
	}
	return All[0], 0
}

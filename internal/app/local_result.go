package app

type LocalResult struct {
	Kind      string
	Title     string
	Fields    []LocalResultField
	Sections  []LocalResultSection
	PlainText string
}

type LocalResultSection struct {
	Title  string
	Fields []LocalResultField
}

type LocalResultField struct {
	Label string
	Value string
	Tone  string
}

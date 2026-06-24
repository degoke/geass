package installer

type Step interface {
	Name() string
	Run() error
}

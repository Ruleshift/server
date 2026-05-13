package metrics

type Recorder interface {
	IncCounter(name string)
	SetGauge(name string, value float64)
	ObserveHistogram(name string, value float64)
}

type NopRecorder struct{}

func (NopRecorder) IncCounter(string) {}

func (NopRecorder) SetGauge(string, float64) {}

func (NopRecorder) ObserveHistogram(string, float64) {}

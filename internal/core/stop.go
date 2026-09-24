package core

// StopError requires caller intervention or inspection, never automatic repair.
// In particular an uncertain click must not be repeated by the model repairer.
type StopError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *StopError) Error() string { return e.Code + ": " + e.Message }

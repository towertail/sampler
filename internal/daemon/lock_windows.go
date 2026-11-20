package daemon

func (h Home) Lock() (*Lock, error) { return nil, ErrUnsupported }

func (l *Lock) Release() error { return nil }

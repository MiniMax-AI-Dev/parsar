package execution

func (r RuntimeProviders) ProviderForEngine(kind string) string {
	if len(r.EngineProviders) == 0 {
		return r.DefaultProvider
	}
	return r.EngineProviders[kind]
}

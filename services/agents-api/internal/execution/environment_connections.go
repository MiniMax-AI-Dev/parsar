package execution

import "context"

// ReplaceEnvironmentConnection begins a serialized transport generation on the owned writer.
func (w *Worker) ReplaceEnvironmentConnection(ctx context.Context, tenant, environment, generation string) error {
	return w.dispatcher.Store.ReplaceEnvironmentConnection(ctx, tenant, environment, generation)
}

// ObserveEnvironmentConnection retains a current transport observation and its event together.
func (w *Worker) ObserveEnvironmentConnection(ctx context.Context, tenant, environment, generation string, revision int64, connected bool) error {
	return w.dispatcher.Store.ObserveEnvironmentConnection(ctx, tenant, environment, generation, revision, connected)
}

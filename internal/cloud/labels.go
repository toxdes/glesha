package cloud

import "context"

type transferLabelKey struct{}

func WithTransferLabel(ctx context.Context, label string) context.Context {
	return context.WithValue(ctx, transferLabelKey{}, label)
}
func transferLabel(ctx context.Context) string {
	label, _ := ctx.Value(transferLabelKey{}).(string)
	if label == "" {
		return "archive"
	}
	return label
}

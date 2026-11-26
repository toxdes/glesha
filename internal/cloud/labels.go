package cloud

import "context"

type transferLabelKey struct{}

func WithTransferLabel(ctx context.Context, label string) context.Context {
	return context.WithValue(ctx, transferLabelKey{}, label)
}

// TransferLabel names the object being transferred in progress messages.
func TransferLabel(ctx context.Context) string {
	label, _ := ctx.Value(transferLabelKey{}).(string)
	if label == "" {
		return "archive"
	}
	return label
}

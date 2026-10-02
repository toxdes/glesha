package backup

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"

	"glesha/crypt"
	"glesha/database/repository"
)

func (s *Service) checkCatalogSecret(ctx context.Context, c repository.Cataloger) error {
	if !s.Config.Catalog.Encrypted || s.Set.CatalogTo == "local" {
		return nil
	}
	check, err := c.GetMeta(ctx, "catalog_key_check")
	if err != nil {
		return err
	}
	if check == "" {
		var encrypted bytes.Buffer
		writer, err := crypt.Encrypt(ctx, &encrypted, s.CatalogPassword)
		if err != nil {
			return err
		}
		_, err = io.WriteString(writer, s.Set.ID)
		closeErr := writer.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		return c.SetMeta(ctx, "catalog_key_check", base64.StdEncoding.EncodeToString(encrypted.Bytes()))
	}
	ciphertext, err := base64.StdEncoding.DecodeString(check)
	if err != nil {
		return fmt.Errorf("backup: invalid catalog secret verifier")
	}
	plain, err := crypt.Decrypt(ctx, bytes.NewReader(ciphertext), s.CatalogPassword)
	if err != nil {
		return fmt.Errorf("backup: catalog passphrase differs: %w", err)
	}
	value, err := io.ReadAll(io.LimitReader(plain, int64(len(s.Set.ID)+1)))
	if err != nil || string(value) != s.Set.ID {
		return fmt.Errorf("backup: catalog passphrase or identity differs")
	}
	return nil
}

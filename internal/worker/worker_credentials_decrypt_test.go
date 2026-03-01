package worker

import (
	"strings"
	"testing"

	"github.com/antoniolg/publisher/internal/publisher"
	"github.com/antoniolg/publisher/internal/secure"
)

func TestLoadCredentialsReturnsDecryptErrorOnCipherMismatch(t *testing.T) {
	store := openWorkerTestStore(t)
	account := createWorkerTestAccount(t, store)

	encryptCipher := newWorkerTestCipher(t)
	saveWorker := Worker{Store: store, Cipher: encryptCipher}
	if err := (workerCredentialsStore{worker: saveWorker}).SaveCredentials(t.Context(), account.ID, publisher.Credentials{
		AccessToken:       "access_mismatch",
		AccessTokenSecret: "secret_mismatch",
		TokenType:         "oauth1",
	}); err != nil {
		t.Fatalf("save credentials: %v", err)
	}

	decryptKey := make([]byte, 32)
	for i := range decryptKey {
		decryptKey[i] = byte(250 - i)
	}
	decryptCipher, err := secure.NewCipher(decryptKey, 1)
	if err != nil {
		t.Fatalf("new decrypt cipher: %v", err)
	}

	loadWorker := Worker{Store: store, Cipher: decryptCipher}
	_, err = loadWorker.loadCredentials(t.Context(), account.ID)
	if err == nil {
		t.Fatalf("expected decrypt error, got nil")
	}
	got := strings.ToLower(err.Error())
	if !strings.Contains(got, "authentication failed") && !strings.Contains(got, "decrypt") {
		t.Fatalf("expected decrypt/authentication error, got %q", err.Error())
	}
}

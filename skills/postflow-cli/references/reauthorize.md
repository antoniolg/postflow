Read when: account reauthentication itself has been explicitly requested

Resolve command paths and bare `scripts/`, `assets/`, `resources/`, and `references/` paths from this skill directory, not from this reference directory. Preserve the operation's authorization and verification requirements.

# Explicitly requested account recovery

Use only after Antonio explicitly authorizes reauthentication of this exact account. A failed publication, missing token, or routine status query is not authorization to start OAuth or switch accounts. Keep failed-publication retries separate from authentication, and inspect the account state again before an authorized retry.

Start OAuth recovery for an account marked as error:

```bash
go run ./cmd/postflow accounts reauthorize --id acc_xxx
```

The command prints the provider authorization URL. Complete that browser flow before requeueing failed publications.

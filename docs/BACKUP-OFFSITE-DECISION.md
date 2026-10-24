# Off-site backup encryption and key custody — decision record

**Status: OPEN — owner decision required.** Nothing here is selected, installed or generated.
Gate G8 (docs/HOSTING-ASSESSMENT.md §6) cannot pass until the owner fills in §5 and the first
off-site copy has been decrypted and verified on a second machine.

## 1. Why a decision is needed

- `deploy/scripts/backup.sh` writes **unencrypted** sets (`db.dump`, `uploads.tar.gz`, `INFO`,
  `SHA256SUMS`, root-only 700/600) to `/backup/rokh` on the same server. Local sets do not protect
  against losing or compromising the server (deploy/README.md §8.3).
- The sets contain personal and business data: customer labels and notes, audit IP addresses,
  staff password hashes, orders and payments. Off-site copies must be encrypted at rest
  (docs/SECURITY.md §9).
- The earlier runbook text used `age`. LRA-2 (owner-observed, 2026-10-24): `age` and `restic` are
  **not installed** on the server, `gpg` is. Whether either tool exists on the owner's PC is
  unknown. Encryption happens on the owner's PC (§8.4), so the PC's tools are what matter; GPG
  being installed does not make `age` commands work.

## 2. Options (none chosen)

| | A. `age` on the owner's PC | B. GnuPG public-key encryption on the owner's PC | C. `restic` repository |
|---|---|---|---|
| Install needed | `age` on the PC (and on any machine that restores) | none on the server; GnuPG on the PC | `restic` on the PC or server; repository password |
| Encrypt | `tar -cf - SET \| age -r <recipient> > SET.tar.age` | `tar -cf - SET \| gpg --encrypt --recipient <key id> --output SET.tar.gpg` | `restic backup SET` |
| Decrypt | `age -d -i <identity file> SET.tar.age \| tar -xf -` | `gpg --decrypt SET.tar.gpg \| tar -xf -` | `restic restore` |
| Key material | one identity file (private key) | one GnuPG key pair (passphrase-protected private key) | repository password (+ key files) |
| Notes | small, single-purpose | widely available; key management is more complex | deduplication and retention built in; most new moving parts |

Public-key options (A, B) let the PC encrypt without holding the decryption key on the same
machine, if the private key is kept offline.

## 3. Requirements for any choice

1. **Key custody.** Who holds the private key or password; where it is stored (e.g. password
   manager plus an offline copy); at least two people or places can recover it; how it is
   rotated; what happens if it is lost (all copies made with it become unreadable).
2. **Pull account.** The current text pulls with `scp root@SERVER`. Decide whether root SSH from the
   PC is acceptable or a dedicated read-only account is created (a server change needing separate
   approval). The server never holds credentials for the off-site destination.
3. **Destinations.** Two places (e.g. the PC and named cloud storage), neither on the server.
4. **Off-site retention.** How many copies and for how long (for example: weekly copies for 8
   weeks, monthly for 12 months), and who deletes old ones.
5. **Freshness.** Copy at least weekly and before every upgrade; a named person; the operations
   log records date, set name and SHA-256 of the encrypted file.
6. **Read-back proof.** Each new key or tool is proven by decrypting a copy on a second machine and
   running `sha256sum -c SHA256SUMS`; a quarterly restore rehearsal uses an off-site copy
   (deploy/README.md §9.2).

## 4. Not decided here

Local encryption of `/backup/rokh` (LRA-2 E3-1) and whether `/backup` is copied by other server
backup tooling remain separate questions for the owner and host.

## 5. Decision (owner fills in)

| Item | Decision |
|---|---|
| Tool (A / B / C / other) | |
| Public key / recipient id (no private material here) | |
| Private key holder(s) and storage | |
| Escrow / second copy of the key | |
| Pull account | |
| Destinations (two) | |
| Off-site retention | |
| Named person and schedule | |
| Owner, date | |

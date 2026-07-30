// Shared password helpers for the account + admin-reset forms.
//
// MIN_PASSWORD_LEN mirrors minPasswordLen in internal/api/auth.go. The server
// is the authority — this only gives immediate feedback so the operator isn't
// told "too short" by a round trip. Keep the two in step.
export const MIN_PASSWORD_LEN = 8

// passwordProblem returns a human-readable reason the password is unacceptable,
// or '' when it passes. Mirrors validatePassword() on the server.
export function passwordProblem(pw: string): string {
  if (pw.length < MIN_PASSWORD_LEN) return `Must be at least ${MIN_PASSWORD_LEN} characters.`
  if (pw.trim() === '') return 'Cannot be blank.'
  return ''
}

// Ambiguous glyphs (O/0, l/1/I) are excluded so a generated password survives
// being read off a screen and typed by hand.
const ALPHABET = 'abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789'

// generatePassword returns a cryptographically random password. Rejection
// sampling keeps the distribution uniform — the naive `% length` would bias
// toward the front of the alphabet.
export function generatePassword(length = 20): string {
  const max = 256 - (256 % ALPHABET.length)
  const out: string[] = []
  const buf = new Uint8Array(1)
  while (out.length < length) {
    crypto.getRandomValues(buf)
    if (buf[0] < max) out.push(ALPHABET[buf[0] % ALPHABET.length])
  }
  return out.join('')
}

// strengthOf gives a coarse 0-3 score for the meter. Deliberately simple and
// honest: it rewards length and character variety, and is NOT a guarantee.
export function strengthOf(pw: string): { score: 0 | 1 | 2 | 3; label: string } {
  if (!pw) return { score: 0, label: '' }
  const classes = [/[a-z]/, /[A-Z]/, /[0-9]/, /[^a-zA-Z0-9]/].filter((re) => re.test(pw)).length
  if (pw.length < MIN_PASSWORD_LEN) return { score: 0, label: 'Too short' }
  if (pw.length >= 16 && classes >= 3) return { score: 3, label: 'Strong' }
  if (pw.length >= 12 && classes >= 2) return { score: 2, label: 'Good' }
  return { score: 1, label: 'Weak' }
}

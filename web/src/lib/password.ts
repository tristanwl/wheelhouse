/** The server's minimum (docs/architecture.md); it also refuses very common passwords. */
export const MIN_PASSWORD_LENGTH = 12;

export interface Strength {
  /** Whether the length requirement is met (the server has the final say). */
  meetsMinimum: boolean;
  /** 0 nothing typed, 1 too short, 2 acceptable, 3 strong. */
  level: 0 | 1 | 2 | 3;
  label: string;
}

/**
 * A hint, not a rule: length is what matters, so there are no composition
 * requirements. The server also checks a list of common passwords.
 */
export function passwordStrength(password: string): Strength {
  const length = [...password].length;
  if (length === 0) return { meetsMinimum: false, level: 0, label: "" };
  if (length < MIN_PASSWORD_LENGTH) {
    const more = MIN_PASSWORD_LENGTH - length;
    return {
      meetsMinimum: false,
      level: 1,
      label: `Too short: ${more} more character${more === 1 ? "" : "s"} needed`,
    };
  }
  const classes = [/[a-z]/, /[A-Z]/, /[0-9]/, /[^a-zA-Z0-9]/].filter((re) => re.test(password));
  const unique = new Set(password).size;
  if (length >= 20 || (length >= 16 && classes.length >= 3 && unique >= 8)) {
    return { meetsMinimum: true, level: 3, label: "Strong" };
  }
  return { meetsMinimum: true, level: 2, label: "Long enough; longer is better" };
}

// Leaves out look-alike characters (0/O, 1/l/I) so it can be read out or typed from a note.
const GENERATED_ALPHABET = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789";

/**
 * A random one-time password for an account an administrator creates or resets,
 * from the browser's secure random source. It is shown once and never stored.
 */
export function generatePassword(length = 20): string {
  const limit = 256 - (256 % GENERATED_ALPHABET.length); // reject the biased tail
  let out = "";
  const buf = new Uint8Array(length * 2);
  while (out.length < length) {
    crypto.getRandomValues(buf);
    for (const b of buf) {
      if (b < limit && out.length < length)
        out += GENERATED_ALPHABET[b % GENERATED_ALPHABET.length];
    }
  }
  return out;
}

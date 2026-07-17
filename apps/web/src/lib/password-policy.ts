export type PasswordPolicyError = "required" | "too_short" | "too_long" | "too_common"

export const PASSWORD_MIN_CHARACTERS = 8
export const PASSWORD_MAX_BYTES = 72

const commonWeakPasswords = new Set([
  "00000000",
  "11111111",
  "11223344",
  "123123123",
  "12341234",
  "12345678",
  "123456789",
  "1234567890",
  "1q2w3e4r",
  "abc12345",
  "abcdefgh",
  "admin123",
  "admin1234",
  "changeme",
  "iloveyou",
  "letmein",
  "monkey123",
  "password",
  "password1",
  "password12",
  "password123",
  "passw0rd",
  "p@ssw0rd",
  "qwerty12",
  "qwerty123",
  "qwertyui",
  "welcome1",
  "welcome123",
])

const encoder = new TextEncoder()

export function validateNewPassword(password: string): PasswordPolicyError | null {
  const chars = Array.from(password)
  if (chars.length === 0) return "required"
  if (chars.length < PASSWORD_MIN_CHARACTERS) return "too_short"
  if (encoder.encode(password).length > PASSWORD_MAX_BYTES) return "too_long"
  if (isCommonWeakPassword(password)) return "too_common"
  return null
}

function isCommonWeakPassword(password: string): boolean {
  const normalized = password.trim().toLowerCase()
  if (normalized === "") return true
  return (
    commonWeakPasswords.has(normalized) ||
    hasOneRepeatedCharacter(normalized) ||
    isSimpleSequence(normalized)
  )
}

function hasOneRepeatedCharacter(value: string): boolean {
  const chars = Array.from(value)
  if (chars.length === 0) return false
  return chars.every((char) => char === chars[0])
}

function isSimpleSequence(value: string): boolean {
  const chars = Array.from(value)
  if (chars.length < PASSWORD_MIN_CHARACTERS) return false
  const digits = chars.every((char) => char >= "0" && char <= "9")
  if (digits) {
    let ascending = true
    let descending = true
    for (let i = 1; i < chars.length; i++) {
      const prev = chars[i - 1].charCodeAt(0)
      const current = chars[i].charCodeAt(0)
      if (current !== prev + 1) ascending = false
      if (current !== prev - 1) descending = false
    }
    if (ascending || descending) return true
  }
  return ["abcdefghijklmnopqrstuvwxyz", "qwertyuiop", "asdfghjkl", "zxcvbnm"].some(
    (sequence) => sequence.includes(value) || reverse(sequence).includes(value),
  )
}

function reverse(value: string): string {
  return Array.from(value).reverse().join("")
}

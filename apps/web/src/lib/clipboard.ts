export async function copyText(text: string, options: { fallback?: boolean } = {}): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // HTTP origins and browser permission policies can reject the modern API.
  }

  if (options.fallback === false) return false

  const textarea = document.createElement("textarea")
  textarea.value = text
  textarea.setAttribute("readonly", "")
  textarea.style.position = "fixed"
  textarea.style.opacity = "0"
  document.body.appendChild(textarea)
  textarea.select()

  try {
    return document.execCommand("copy")
  } catch {
    return false
  } finally {
    textarea.remove()
  }
}

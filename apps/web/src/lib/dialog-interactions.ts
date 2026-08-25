const CREDENTIAL_KIND_MENU_SELECTOR = "[data-credential-kind-menu]"

export function preventDialogDismissForCredentialMenu(event: Event): void {
  if (event.target instanceof Element && event.target.closest(CREDENTIAL_KIND_MENU_SELECTOR)) {
    event.preventDefault()
  }
}

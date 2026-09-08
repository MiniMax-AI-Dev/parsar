/**
 * The Agent rail's sections. It is `RailSection`, not `PageSection`: these
 * render inside the detail rail, where a head is a 12px eyebrow. Aliased
 * rather than re-declared so the rail can never drift to a second size.
 */
export { RailSection as DetailSection } from "../../../components/ui/detail-rail"
export { InlineError } from "../../../components/ui/error-state"

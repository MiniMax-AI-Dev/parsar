/**
 * The Agent rail's sections. It is `RailSection`, not `PageSection`: these
 * render inside the detail rail, which runs its own 14px head. Aliased rather
 * than re-declared so the rail can never drift to a second size.
 */
export { RailSection as DetailSection } from "../../../components/ui/detail-rail"
export { InlineError } from "../../../components/ui/error-state"

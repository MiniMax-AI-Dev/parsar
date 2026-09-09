import { NextResponse, type NextRequest } from "next/server"
import { i18n } from "@/lib/i18n"

export default function middleware(request: NextRequest) {
  const pathname = request.nextUrl.pathname
  const pathLocale = i18n.languages.find(
    (locale) => pathname === `/${locale}` || pathname.startsWith(`/${locale}/`),
  )

  if (!pathLocale) {
    const target = `/${i18n.defaultLanguage}${pathname === "/" ? "" : pathname}`
    return NextResponse.rewrite(new URL(target, request.url))
  }

  if (pathLocale === i18n.defaultLanguage) {
    const stripped = pathname.slice(`/${pathLocale}`.length)
    const target = stripped || "/"
    const url = new URL(target, request.url)
    url.search = request.nextUrl.search
    return NextResponse.redirect(url)
  }

  return NextResponse.next()
}

export const config = {
  matcher: ["/((?!api|_next/static|_next/image|favicon.ico|icon.png).*)", "/"],
}

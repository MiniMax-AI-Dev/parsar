import type { ReactNode } from "react"
import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { cn } from "../../lib/utils"
import { useAdminView, type AdminView } from "../../lib/admin-router"
import {
  MessageSquare, Play, PackageSearch,
  Bot, Wrench, Database,
  Box,
  BookText,
  BrainCircuit,
  Plug,
  Users, Key, LineChart, ShieldCheck, Settings,
  ChevronDown,
  type LucideIcon,
} from "lucide-react"
import { LanguageSwitcher } from "./LanguageSwitcher"
import { WorkspaceSwitcher } from "./WorkspaceSwitcher"
import { UserMenu } from "./UserMenu"
import { useWorkspaceProjects } from "../../lib/api-workspaces"
import {
  setProjectId,
  useProjectId,
  useWorkspaceId,
} from "../../lib/workspace"

interface AdminLayoutProps {
  children: ReactNode
  activeMenu?: string
  fullBleed?: boolean
  hideSidebar?: boolean
  contentClassName?: string
}

interface MenuItem {
  id: AdminView
  /** key under nav.items.* — kept English to lock product semantics */
  itemKey: string
  icon: LucideIcon
  badge?: number | string
  hint?: "p1Hint"
}

interface MenuGroup {
  /** key under nav.*Group */
  groupKey: string
  items: MenuItem[]
}

const menuGroups: MenuGroup[] = [
  {
    groupKey: "collaborationGroup",
    items: [
      { id: "conversations", itemKey: "conversations", icon: MessageSquare },
      { id: "runs", itemKey: "runs", icon: Play },
      { id: "artifacts", itemKey: "artifacts", icon: PackageSearch },
    ],
  },
  {
    groupKey: "agentManagementGroup",
    items: [
      { id: "agents", itemKey: "agents", icon: Bot },
      { id: "connections", itemKey: "connections", icon: Plug },
      { id: "capabilities", itemKey: "capabilities", icon: Wrench },
      { id: "models", itemKey: "models", icon: Database },
    ],
  },
  {
    groupKey: "ingressGroup",
    items: [
      { id: "runtime", itemKey: "runtime", icon: Box },
    ],
  },
  {
    groupKey: "assetsGroup",
    items: [
      { id: "specs", itemKey: "specs", icon: BookText },
      { id: "memory", itemKey: "memory", icon: BrainCircuit },
    ],
  },
  {
    groupKey: "governanceGroup",
    items: [
      { id: "members", itemKey: "members", icon: Users },
      { id: "secrets", itemKey: "secrets", icon: Key },
      { id: "usage", itemKey: "usage", icon: LineChart },
      { id: "audit", itemKey: "audit", icon: ShieldCheck },
      { id: "settings", itemKey: "settings", icon: Settings },
    ],
  },
]

export function AdminLayout({
  children,
  activeMenu = "agents",
  fullBleed = false,
  hideSidebar = false,
  contentClassName,
}: AdminLayoutProps) {
  const { t } = useTranslation("common")
  const { navigate } = useAdminView()
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({})
  const toggle = (key: string) =>
    setCollapsed((prev) => ({ ...prev, [key]: !prev[key] }))

  // Self-heal stale pid when the bound workspace doesn't own it.
  // setWorkspaceId clears pid on dropdown/URL changes, but a pid from
  // an earlier session can survive a page reload; without this, project-
  // scoped hooks would surface another workspace's data.
  const wsId = useWorkspaceId()
  const pid = useProjectId()
  const projectsQ = useWorkspaceProjects(wsId)
  useEffect(() => {
    if (!wsId) return
    if (projectsQ.isLoading || projectsQ.isError || !projectsQ.data) return

    if (!pid) {
      if (projectsQ.data.projects.length === 1) {
        setProjectId(projectsQ.data.projects[0].id)
      }
      return
    }

    const owned = projectsQ.data.projects.some((p) => p.id === pid)
    if (!owned) setProjectId(null)
  }, [wsId, pid, projectsQ.isLoading, projectsQ.isError, projectsQ.data])

  return (
    <div className="flex h-screen flex-col overflow-hidden bg-slate-50/60 text-slate-900 antialiased">
      <header className="flex h-14 shrink-0 items-center gap-3 border-b border-slate-200/70 bg-white px-4">
        <div className="flex items-center gap-2">
          <img
            src="/favicon.png"
            alt=""
            className="h-7 w-7"
          />
          <span className="text-[13px] font-semibold tracking-display">{t("appName")}</span>
        </div>

        <WorkspaceSwitcher />

        <div className="ml-auto flex items-center gap-2">
          <LanguageSwitcher />
          <UserMenu />
        </div>
      </header>

      <div className="flex flex-1 overflow-hidden">
        {!hideSidebar && <aside className="flex w-60 shrink-0 flex-col gap-4 overflow-y-auto border-r border-slate-200/70 bg-white px-2.5 py-3">
          {menuGroups.map((group, idx) => {
            const isCollapsed = !!collapsed[group.groupKey]
            return (
              <nav
                key={group.groupKey}
                className={cn(
                  "flex flex-col gap-px",
                  idx === 0 ? "mt-0" : "mt-1.5"
                )}
              >
                <button
                  type="button"
                  onClick={() => toggle(group.groupKey)}
                  className="group/header mb-0.5 flex h-5 w-full items-center gap-1 rounded px-2 text-[12px] font-normal text-slate-500 transition-colors hover:text-slate-700"
                >
                  <span>
                    {t(`nav.${group.groupKey}` as never)}
                  </span>
                  <ChevronDown
                    className={cn(
                      "h-3 w-3 text-slate-400 transition-transform group-hover/header:text-slate-500",
                      isCollapsed && "-rotate-90"
                    )}
                    strokeWidth={2}
                  />
                </button>
                {!isCollapsed &&
                  group.items.map((item) => {
                    const Icon = item.icon
                    const isActive = activeMenu === item.id
                    return (
                      <button
                        key={item.id}
                        type="button"
                        onClick={() => navigate(item.id)}
                        className={cn(
                          "group flex h-8 w-full items-center gap-2.5 rounded-md px-2 text-[14px] transition-colors",
                          isActive
                            ? "bg-slate-100 font-medium text-slate-900"
                            : "font-normal text-slate-700 hover:bg-slate-100/60 hover:text-slate-900"
                        )}
                      >
                        <Icon
                          className={cn(
                            "h-4 w-4 shrink-0",
                            isActive
                              ? "text-slate-700"
                              : "text-slate-400 group-hover:text-slate-600"
                          )}
                          strokeWidth={1.75}
                        />
                        <span className="truncate">
                          {t(`nav.items.${item.itemKey}` as never)}
                        </span>
                        {item.badge !== undefined && (
                          <span className="ml-auto inline-flex h-[18px] min-w-[18px] items-center justify-center rounded-full bg-amber-100 px-1.5 text-[10.5px] font-medium text-amber-700">
                            {item.badge}
                          </span>
                        )}
                        {item.hint && (
                          <span className="ml-auto text-[10px] uppercase tracking-wider text-slate-400">
                            {t(`nav.${item.hint}` as never)}
                          </span>
                        )}
                      </button>
                    )
                  })}
              </nav>
            )
          })}
        </aside>}

        <main className="flex-1 overflow-y-auto">
          {fullBleed ? (
            children
          ) : (
            <div className={cn("mx-auto max-w-screen-2xl px-8 py-8", contentClassName)}>
              {children}
            </div>
          )}
        </main>
      </div>
    </div>
  )
}

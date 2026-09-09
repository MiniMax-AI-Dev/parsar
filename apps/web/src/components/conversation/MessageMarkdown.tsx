import ReactMarkdown from "react-markdown"

export function MessageMarkdown({ content }: { content: string }) {
  return (
    <div className="prose prose-sm min-w-0 max-w-none text-fg [overflow-wrap:anywhere] prose-p:my-1.5 prose-pre:my-3 prose-pre:whitespace-pre-wrap prose-pre:break-all prose-pre:rounded-md prose-pre:bg-surface-muted prose-pre:text-fg prose-code:text-fg prose-code:before:content-none prose-code:after:content-none prose-a:text-fg prose-a:underline prose-a:underline-offset-4 prose-blockquote:text-fg prose-blockquote:border-line prose-headings:text-fg prose-strong:text-fg prose-strong:font-medium">
      <ReactMarkdown components={{ h1: ({ children }) => <h2>{children}</h2> }}>
        {content}
      </ReactMarkdown>
    </div>
  )
}

package agentdaemon

import (
	"context"
	"strings"
)

// Auto-mounted fetch_chat_history MCP tool.
//
// Every agent_daemon-backed Claude Code agent gets this stdio MCP server for
// free — no capability row, no user configuration. The server is a tiny proxy:
// on tools/call it does an HTTP GET back to our internal history endpoint
// (IMHistoryEndpoint) carrying a per-conversation HMAC bearer token minted by
// the connector. Tokens + IM SDK logic stay server-side; the sandbox only ever
// sees a scoped URL + token.
//
// Delivery is a single `sh -c` front door so nothing has to be pre-installed
// in the sandbox image:
//
//   - node present → hand the stdio session to an inline JS MCP proxy that
//     speaks JSON-RPC and performs the HTTP call.
//   - node absent  → a minimal POSIX-sh MCP responder still initializes and
//     lists the tool, but every tools/call returns a friendly "install
//     Node.js" error. This keeps agent startup from ever blocking while still
//     surfacing the requirement through a normal tool call, exactly when the
//     agent tries to use it.

// imHistoryServerName is the mcpServers map key; Claude Code exposes the tool
// as mcp__parsar_chat_history__fetch_chat_history.
const imHistoryServerName = "parsar_chat_history"

// imHistoryMCPScript is the sh front door. It is stored as a raw string, so it
// MUST NOT contain backtick characters — the inline JS therefore uses only
// double-quoted strings and + concatenation (no template literals), and no
// single quotes (it lives inside a single-quoted `node -e '…'`).
const imHistoryMCPScript = `
if command -v node >/dev/null 2>&1; then
  exec node -e '
var http=require("http"),https=require("https");
var BASE=process.env.PARSAR_IM_HISTORY_URL||"";
var TOKEN=process.env.PARSAR_IM_HISTORY_TOKEN||"";
var CONV=process.env.PARSAR_CONVERSATION_ID||"";
var TOOL={name:"fetch_chat_history",description:"Fetch recent IM group-chat history for the CURRENT conversation across Feishu/Slack/Discord/Teams. Returns messages oldest-first as JSON. Use limit to request a page size (silently clamped to the platform cap; Feishu 50, Slack 15, Discord 100, Teams 50 per page) and next_cursor from a prior response to page further back. Optional thread_id scopes history to one platform-native thread (Slack thread_ts / Discord thread channel id / Teams reply message id).",inputSchema:{type:"object",properties:{limit:{type:"integer",description:"Max messages to return (clamped to platform cap, e.g. 50 on Feishu/Teams, 15 on Slack, 100 on Discord)."},cursor:{type:"string",description:"Opaque next_cursor from a previous call to fetch older messages."},thread_id:{type:"string",description:"Optional platform-native thread id to scope history. Slack thread_ts, Discord thread channel id, or Teams reply message id. Empty/unset = whole chat."}}}};
function send(m){process.stdout.write(JSON.stringify(m)+"\n");}
function doFetch(a,cb){
  var u;
  try{u=new URL(BASE);}catch(e){cb(e);return;}
  u.searchParams.set("conversation_id",CONV);
  if(a&&a.limit!=null)u.searchParams.set("limit",String(a.limit));
  if(a&&a.cursor)u.searchParams.set("cursor",String(a.cursor));
  if(a&&a.thread_id)u.searchParams.set("thread_id",String(a.thread_id));
  var lib=u.protocol==="https:"?https:http;
  var rq=lib.request(u,{method:"GET",headers:{Authorization:"Bearer "+TOKEN}},function(res){var b="";res.on("data",function(d){b+=d;});res.on("end",function(){cb(null,b);});});
  rq.on("error",function(e){cb(e);});
  rq.end();
}
function handle(m){
  var id=m.id,method=m.method;
  if(method==="initialize"){var pv=(m.params&&m.params.protocolVersion)||"2024-11-05";send({jsonrpc:"2.0",id:id,result:{protocolVersion:pv,capabilities:{tools:{}},serverInfo:{name:"parsar-chat-history",version:"0.1.0"}}});return;}
  if(typeof method==="string"&&method.indexOf("notifications/")===0)return;
  if(method==="ping"){send({jsonrpc:"2.0",id:id,result:{}});return;}
  if(method==="tools/list"){send({jsonrpc:"2.0",id:id,result:{tools:[TOOL]}});return;}
  if(method==="tools/call"){
    var p=m.params||{};
    if(p.name!=="fetch_chat_history"){send({jsonrpc:"2.0",id:id,error:{code:-32602,message:"unknown tool"}});return;}
    doFetch(p.arguments||{},function(err,body){
      if(err){send({jsonrpc:"2.0",id:id,result:{content:[{type:"text",text:"fetch chat history failed: "+String((err&&err.message)||err)}],isError:true}});return;}
      send({jsonrpc:"2.0",id:id,result:{content:[{type:"text",text:body}]}});
    });
    return;
  }
  if(id!==undefined&&id!==null){send({jsonrpc:"2.0",id:id,error:{code:-32601,message:"method not found"}});}
}
var buf="";
process.stdin.on("data",function(c){
  buf+=c;var i;
  while((i=buf.indexOf("\n"))>=0){
    var line=buf.slice(0,i);buf=buf.slice(i+1);
    if(!line.trim())continue;
    var m;try{m=JSON.parse(line);}catch(e){continue;}
    handle(m);
  }
});
process.stdin.resume();
'
fi
while IFS= read -r line; do
  id=${line#*\"id\":}
  id=${id%%,*}
  id=${id%%\}*}
  id=${id# }
  id=${id# }
  case "$line" in
    *'"initialize"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"parsar-chat-history","version":"0.1.0"}}}\n' "${id:-0}"
      ;;
    *'"tools/list"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"fetch_chat_history","description":"Fetch recent IM group-chat history for the current conversation.","inputSchema":{"type":"object","properties":{"limit":{"type":"integer"},"cursor":{"type":"string"},"thread_id":{"type":"string","description":"Optional platform-native thread id (Slack thread_ts / Discord thread channel id / Teams reply message id)."}}}}]}}\n' "${id:-0}"
      ;;
    *'"tools/call"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"Node.js is not installed on this machine, so chat history cannot be fetched. Please install Node.js and try again."}],"isError":true}}\n' "${id:-0}"
      ;;
  esac
done
`

// imHistoryMCPServer builds the auto-mounted fetch_chat_history MCP entry for a
// conversation, or (,"",false) when the tool is disabled (no endpoint/signer
// configured, or no conversation to scope the token to). The returned value is
// the same map shape resolveMCPCapability produces, so it merges into
// result.MCPServers uniformly.
func (c *Connector) imHistoryMCPServer(conversationID string) (name string, entry map[string]any, ok bool) {
	if c.imHistoryEndpoint == "" || c.imHistoryToken == nil || conversationID == "" {
		return "", nil, false
	}
	token := c.imHistoryToken(conversationID)
	if token == "" {
		return "", nil, false
	}
	entry = map[string]any{
		"command": "sh",
		"args":    []string{"-c", imHistoryMCPScript},
		"env": map[string]string{
			"PARSAR_IM_HISTORY_URL":   c.imHistoryEndpoint,
			"PARSAR_IM_HISTORY_TOKEN": token,
			"PARSAR_CONVERSATION_ID":  conversationID,
		},
	}
	return imHistoryServerName, entry, true
}

// imHistoryEnabledForAgent reports whether the built-in fetch_chat_history tool
// should be injected for this agent. Built-ins default to ON, so a nil store,
// an empty agent id, or a lookup error all resolve to enabled — a bookkeeping
// failure must never silently strip the tool. Only an explicit disabled flag
// (agent_builtin_capabilities.enabled = false) suppresses injection.
func (c *Connector) imHistoryEnabledForAgent(ctx context.Context, agentID string) bool {
	if c.capabilities == nil || strings.TrimSpace(agentID) == "" {
		return true
	}
	enabled, err := c.capabilities.IsBuiltinCapabilityEnabled(ctx, agentID, imHistoryServerName)
	if err != nil {
		c.log.Debug("agent_daemon: im-history builtin flag lookup failed; defaulting on", "agent_id", agentID, "err", err)
		return true
	}
	return enabled
}

import { useQuery } from '@tanstack/react-query'
import { modelMappingsApi, routersApi, proxyChat } from '../api/client'
import { ModelMap, Router } from '../types'
import { Send, Trash2, ChevronDown, ChevronUp, Bot, User, Loader2, Brain } from 'lucide-react'
import { useState, useRef, useEffect } from 'react'

interface ChatMessage {
  role: 'user' | 'assistant'
  content: string           // response text
  thinkingContent?: string  // reasoning_content from the backend
  thinkingDone?: boolean    // false = still streaming the think block
  thinkingTokens?: number
  latencyMs?: number
  genMs?: number            // ms spent generating response tokens (excludes thinking time)
  tokens?: number
  error?: boolean
  streaming?: boolean
}

// ── Think block ────────────────────────────────────────────────────────────────

function ThinkBlock({ content, streaming, tokenCount }: {
  content: string
  streaming: boolean
  tokenCount?: number
}) {
  const [open, setOpen] = useState(true)

  // Auto-collapse when thinking finishes
  useEffect(() => {
    if (!streaming) setOpen(false)
  }, [streaming])

  return (
    <div className="mb-2 rounded-lg border border-purple-800/40 bg-purple-950/20 overflow-hidden text-xs">
      <button
        onClick={() => setOpen(o => !o)}
        className="w-full flex items-center gap-2 px-3 py-2 text-purple-300/70 hover:text-purple-200 text-left"
      >
        {streaming
          ? <Loader2 className="w-3 h-3 animate-spin flex-shrink-0" />
          : <Brain className="w-3 h-3 flex-shrink-0" />
        }
        <span className="font-medium">
          {streaming ? 'Thinking…' : `Thought${tokenCount ? ` · ~${tokenCount} tokens` : ''}`}
        </span>
        {streaming && <span className="inline-block w-0.5 h-3 bg-purple-300/50 animate-pulse" />}
        <span className="ml-auto opacity-60">{open ? <ChevronUp className="w-3 h-3" /> : <ChevronDown className="w-3 h-3" />}</span>
      </button>
      {open && (
        <div className="px-3 pb-3 pt-1 text-purple-200/55 italic leading-relaxed whitespace-pre-wrap border-t border-purple-800/30 font-mono max-h-56 overflow-y-auto">
          {content}
          {streaming && <span className="inline-block w-0.5 h-3 bg-purple-300/50 animate-pulse ml-0.5" />}
        </div>
      )}
    </div>
  )
}

// ── Model picker ───────────────────────────────────────────────────────────────

function ModelPicker({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const { data: maps = [] }    = useQuery<ModelMap[]>({ queryKey: ['model-mappings'], queryFn: modelMappingsApi.list })
  const { data: routers = [] } = useQuery<Router[]>({ queryKey: ['routers'], queryFn: routersApi.list })
  const models = Array.from(new Set(maps.map(m => m.local_model))).sort()

  return (
    <select
      value={value}
      onChange={e => onChange(e.target.value)}
      className="px-3 py-1.5 bg-slate-700 border border-slate-600 rounded-lg text-sm text-white font-mono focus:outline-none focus:border-blue-500 min-w-48"
    >
      <option value="">— select model —</option>
      {models.length > 0 && (
        <optgroup label="Models">
          {models.map(m => <option key={m} value={m}>{m}</option>)}
        </optgroup>
      )}
      {routers.length > 0 && (
        <optgroup label="Routers">
          {routers.map(r => <option key={`r:${r.name}`} value={r.name}>{r.name}</option>)}
        </optgroup>
      )}
    </select>
  )
}

// ── Message bubble ─────────────────────────────────────────────────────────────

function MessageBubble({ msg }: { msg: ChatMessage }) {
  const isUser = msg.role === 'user'

  return (
    <div className={`flex gap-3 ${isUser ? 'flex-row-reverse' : ''}`}>
      <div className={`flex-shrink-0 w-7 h-7 rounded-full flex items-center justify-center mt-0.5 ${
        isUser ? 'bg-blue-600' : 'bg-slate-700'
      }`}>
        {isUser ? <User className="w-3.5 h-3.5" /> : <Bot className="w-3.5 h-3.5 text-slate-300" />}
      </div>

      <div className={`flex flex-col gap-1 max-w-[80%] ${isUser ? 'items-end' : 'items-start'}`}>
        {/* Thinking block — shown while reasoning or once done */}
        {!isUser && !!msg.thinkingContent && (
          <ThinkBlock
            content={msg.thinkingContent}
            streaming={msg.thinkingDone === false}
            tokenCount={msg.thinkingTokens}
          />
        )}

        {/* Response bubble — hidden while thinking is still streaming */}
        {(isUser || msg.thinkingDone !== false) && (
          <div className={`px-4 py-3 rounded-2xl text-sm leading-relaxed whitespace-pre-wrap ${
            isUser
              ? 'bg-blue-600 text-white rounded-tr-sm'
              : msg.error
                ? 'bg-red-900/40 text-red-300 border border-red-700/40 rounded-tl-sm'
                : 'bg-slate-700 text-slate-100 rounded-tl-sm'
          }`}>
            {msg.content || (msg.streaming ? '' : '…')}
            {msg.streaming && (
              <span className="inline-block w-0.5 h-4 bg-slate-300 ml-0.5 animate-pulse align-text-bottom" />
            )}
          </div>
        )}

        {/* Stats */}
        {!isUser && !msg.streaming && (msg.latencyMs !== undefined || msg.tokens !== undefined) && (
          <div className="flex flex-wrap gap-3 text-xs text-slate-500 px-1">
            {msg.latencyMs !== undefined && <span>{(msg.latencyMs / 1000).toFixed(1)}s</span>}
            {msg.thinkingTokens !== undefined && msg.thinkingTokens > 0 && (
              <span className="text-purple-400/70">~{msg.thinkingTokens} think</span>
            )}
            {msg.tokens !== undefined && (
              <span>{msg.tokens} tok</span>
            )}
            {msg.tokens && msg.genMs
              ? <span className="text-cyan-400/80">{((msg.tokens / msg.genMs) * 1000).toFixed(0)} tok/s</span>
              : msg.tokens && msg.latencyMs
              ? <span className="text-cyan-400/80">{((msg.tokens / msg.latencyMs) * 1000).toFixed(0)} tok/s</span>
              : null}
          </div>
        )}
      </div>
    </div>
  )
}

// ── Page ───────────────────────────────────────────────────────────────────────

export default function Chat() {
  const [model, setModel]               = useState('')
  const [messages, setMessages]         = useState<ChatMessage[]>([])
  const [input, setInput]               = useState('')
  const [systemPrompt, setSystemPrompt] = useState('')
  const [showSys, setShowSys]           = useState(false)
  const [thinking, setThinking]         = useState<boolean | null>(null)
  const [streaming, setStreaming]       = useState(false)
  const bottomRef = useRef<HTMLDivElement>(null)
  const abortRef  = useRef<AbortController | null>(null)
  const inputRef  = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  async function send() {
    const text = input.trim()
    if (!text || !model || streaming) return

    const userMsg: ChatMessage = { role: 'user', content: text }
    const assistantMsg: ChatMessage = { role: 'assistant', content: '', streaming: true }

    setMessages(prev => [...prev, userMsg, assistantMsg])
    setInput('')
    setStreaming(true)

    const controller = new AbortController()
    abortRef.current = controller
    const start = Date.now()

    const apiMessages = [
      ...(systemPrompt.trim() ? [{ role: 'system', content: systemPrompt.trim() }] : []),
      ...messages.map(m => ({ role: m.role, content: m.content })),
      { role: 'user', content: text },
    ]

    try {
      const extraParams = thinking === null
        ? undefined
        : { chat_template_kwargs: { enable_thinking: thinking } }
      const res = await proxyChat(model, apiMessages, true, controller.signal, extraParams)

      if (!res.ok) {
        const err = await res.json().catch(() => ({ error: { message: `HTTP ${res.status}` } }))
        const trace = err.error?.trace ?? res.headers.get('X-Trace-Id')
        const suffix = trace ? ` [T-${trace}]` : ''
        setMessages(prev => prev.map((m, i) =>
          i === prev.length - 1
            ? { ...m, content: (err.error?.message ?? 'Request failed') + suffix, streaming: false, error: true }
            : m))
        return
      }

      const reader = res.body!.getReader()
      const decoder = new TextDecoder()
      let content = ''
      let thinkingContent = ''
      let usage: { completion_tokens?: number } | null = null
      let buf = ''
      let firstContentMs: number | null = null

      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        const lines = buf.split('\n')
        buf = lines.pop() ?? ''

        for (const line of lines) {
          if (!line.startsWith('data: ')) continue
          const data = line.slice(6).trim()
          if (data === '[DONE]') continue
          try {
            const evt = JSON.parse(data)
            const delta = evt.choices?.[0]?.delta
            // Thinking content arrives in delta.reasoning_content (llama.cpp / vllm)
            if (delta?.reasoning_content) {
              thinkingContent += delta.reasoning_content
              setMessages(prev => prev.map((m, i) =>
                i === prev.length - 1
                  ? { ...m, thinkingContent, thinkingDone: false }
                  : m))
            }
            // Response content arrives in delta.content once thinking is done
            if (delta?.content) {
              if (firstContentMs === null) firstContentMs = Date.now()
              content += delta.content
              setMessages(prev => prev.map((m, i) =>
                i === prev.length - 1
                  ? { ...m, content, thinkingDone: true }
                  : m))
            }
            if (evt.usage) usage = evt.usage
          } catch { /* partial JSON */ }
        }
      }

      const endMs = Date.now()
      const totalLatencyMs = endMs - start
      // genMs is the time spent generating response tokens (excludes thinking time)
      const genMs = firstContentMs !== null ? endMs - firstContentMs : totalLatencyMs

      setMessages(prev => prev.map((m, i) =>
        i === prev.length - 1 ? {
          ...m,
          content,
          thinkingContent: thinkingContent || undefined,
          thinkingDone: true,
          thinkingTokens: thinkingContent ? Math.round(thinkingContent.length / 4) : undefined,
          streaming: false,
          latencyMs: totalLatencyMs,
          genMs,
          tokens: usage?.completion_tokens,
        } : m))
    } catch (e: any) {
      if (e.name === 'AbortError') {
        setMessages(prev => prev.map((m, i) =>
          i === prev.length - 1
            ? { ...m, content: m.content || '(cancelled)', streaming: false, thinkingDone: true,
                thinkingContent: m.thinkingContent || undefined }
            : m))
      } else {
        setMessages(prev => prev.map((m, i) =>
          i === prev.length - 1
            ? { ...m, content: e.message, streaming: false, error: true, thinkingDone: true }
            : m))
      }
    } finally {
      setStreaming(false)
      abortRef.current = null
      setTimeout(() => inputRef.current?.focus(), 0)
    }
  }

  function stop() { abortRef.current?.abort() }

  function handleKey(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send() }
  }

  return (
    <div className="flex flex-col h-[calc(100vh-4rem)] md:h-[calc(100vh-2rem)] max-h-[900px]">
      {/* ── Header ── */}
      <div className="flex flex-wrap items-center gap-3 mb-3">
        <h2 className="text-2xl font-bold">Chat</h2>
        <ModelPicker value={model} onChange={setModel} />
        <button
          onClick={() => setThinking(t => t === null ? true : t ? false : null)}
          title={
            thinking === null
              ? 'Using model default — click to force thinking on'
              : thinking
                ? 'Thinking forced on — click to force off'
                : 'Thinking forced off — click to use model default'
          }
          className={`flex items-center gap-1.5 px-2 py-1.5 text-xs rounded-lg border transition-colors ${
            thinking === null
              ? 'text-slate-400 bg-slate-800 border-slate-700 hover:text-white'
              : thinking
                ? 'text-purple-300 bg-purple-950/30 border-purple-700/50 hover:border-purple-600'
                : 'text-slate-300 bg-slate-900 border-slate-600 hover:text-white'
          }`}
        >
          <Brain className="w-3 h-3" />
          {thinking === null ? 'Think: default' : thinking ? 'Think: on' : 'Think: off'}
        </button>
        <button
          onClick={() => setShowSys(s => !s)}
          className="flex items-center gap-1 px-2 py-1.5 text-xs text-slate-400 hover:text-white bg-slate-800 border border-slate-700 rounded-lg"
        >
          System {showSys ? <ChevronUp className="w-3 h-3" /> : <ChevronDown className="w-3 h-3" />}
        </button>
        {messages.length > 0 && (
          <button
            onClick={() => setMessages([])}
            className="flex items-center gap-1 px-2 py-1.5 text-xs text-slate-400 hover:text-red-400 bg-slate-800 border border-slate-700 rounded-lg ml-auto"
          >
            <Trash2 className="w-3 h-3" /> Clear
          </button>
        )}
      </div>

      {showSys && (
        <textarea
          value={systemPrompt}
          onChange={e => setSystemPrompt(e.target.value)}
          placeholder="System prompt (optional)…"
          rows={3}
          className="mb-3 w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-sm text-slate-200 placeholder-slate-500 resize-none focus:outline-none focus:border-blue-500"
        />
      )}

      {/* ── Messages ── */}
      <div className="flex-1 overflow-y-auto space-y-5 py-2 pr-1 min-h-0">
        {messages.length === 0 ? (
          <div className="flex flex-col items-center justify-center h-full text-slate-500 select-none">
            <Bot className="w-10 h-10 mb-3 opacity-30" />
            <p className="text-sm">Select a model and start chatting</p>
          </div>
        ) : (
          messages.map((msg, i) => <MessageBubble key={i} msg={msg} />)
        )}
        <div ref={bottomRef} />
      </div>

      {/* ── Input ── */}
      <div className="mt-3 flex gap-2 items-end">
        <textarea
          ref={inputRef}
          value={input}
          onChange={e => setInput(e.target.value)}
          onKeyDown={handleKey}
          disabled={!model}
          placeholder={model ? 'Message… (Enter to send, Shift+Enter for newline)' : 'Select a model first'}
          rows={1}
          style={{ resize: 'none' }}
          className="flex-1 px-4 py-3 bg-slate-800 border border-slate-700 rounded-xl text-sm text-slate-100 placeholder-slate-500 focus:outline-none focus:border-blue-500 disabled:opacity-40 overflow-hidden"
          onInput={e => {
            const el = e.currentTarget
            el.style.height = 'auto'
            el.style.height = Math.min(el.scrollHeight, 160) + 'px'
          }}
        />
        {streaming ? (
          <button onClick={stop} className="p-3 bg-red-600 hover:bg-red-700 rounded-xl text-white flex-shrink-0" title="Stop">
            <Loader2 className="w-4 h-4 animate-spin" />
          </button>
        ) : (
          <button
            onClick={send}
            disabled={!model || !input.trim()}
            className="p-3 bg-blue-600 hover:bg-blue-700 disabled:opacity-40 rounded-xl text-white flex-shrink-0"
            title="Send"
          >
            <Send className="w-4 h-4" />
          </button>
        )}
      </div>
    </div>
  )
}

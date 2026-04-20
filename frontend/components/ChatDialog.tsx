"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { MessageSquare } from "lucide-react";
import MessageList from "./MessageList";
import InputBar from "./InputBar";
import { useWebSocket } from "@/hooks/useWebSocket";
import { fetchMessages } from "@/utils/api";
import type { Message, StreamBlock, TodoItem } from "@/types/chat";

interface ChatDialogProps {
  conversationId: string | null;
  title?: string | null;
  onTitleUpdated?: (id: string, title: string) => void;
}

export default function ChatDialog({ conversationId, title, onTitleUpdated }: ChatDialogProps) {
  const [messages, setMessages] = useState<Message[]>([]);
  const [streamBlocks, setStreamBlocks] = useState<StreamBlock[]>([]);
  const [isStreaming, setIsStreaming] = useState(false);
  const streamBlocksRef = useRef<StreamBlock[]>([]);

  const applyUpdate = useCallback((updater: (prev: StreamBlock[]) => StreamBlock[]) => {
    const next = updater(streamBlocksRef.current);
    streamBlocksRef.current = next;
    setStreamBlocks(next);
  }, []);

  const loadMessages = useCallback(async (convId: string) => {
    try {
      const msgs = await fetchMessages(convId);
      setMessages(msgs ?? []);
    } catch {
      setMessages([]);
    }
  }, []);

  useEffect(() => {
    setMessages([]);
    streamBlocksRef.current = [];
    setStreamBlocks([]);
    setIsStreaming(false);
    if (conversationId) {
      loadMessages(conversationId);
    }
  }, [conversationId, loadMessages]);

  const handleToken = useCallback((text: string) => {
    setIsStreaming(true);
    applyUpdate((prev) => {
      const last = prev[prev.length - 1];
      if (last?.type === "text") {
        return [...prev.slice(0, -1), { type: "text", content: last.content + text }];
      }
      return [...prev, { type: "text", content: text }];
    });
  }, [applyUpdate]);

  const handleThinkingDelta = useCallback((text: string) => {
    applyUpdate((prev) => {
      const last = prev[prev.length - 1];
      if (last?.type === "thinking" && last.streaming) {
        return [...prev.slice(0, -1), { ...last, content: last.content + text }];
      }
      return [...prev, { type: "thinking", content: text, streaming: true }];
    });
  }, [applyUpdate]);

  const handleToolUse = useCallback((id: string, name: string, input: string) => {
    applyUpdate((prev) => [
      ...prev.map((b) =>
        b.type === "thinking" && b.streaming ? { ...b, streaming: false } : b
      ),
      { type: "tool_use", id, name, input, status: "running" as const },
    ]);
  }, [applyUpdate]);

  const handleToolResult = useCallback((toolId: string, content: string) => {
    applyUpdate((prev) =>
      prev.map((b) =>
        b.type === "tool_use" && b.id === toolId
          ? { ...b, status: "done" as const, result: content }
          : b
      )
    );
  }, [applyUpdate]);

  const handleTodoUpdate = useCallback((id: string, _name: string, input: string) => {
    try {
      const parsed = JSON.parse(input) as { todos?: TodoItem[] };
      const todos: TodoItem[] = parsed.todos ?? [];
      applyUpdate((prev) => {
        const idx = prev.findIndex((b) => b.type === "todo" && b.id === id);
        if (idx >= 0) {
          const updated = [...prev];
          updated[idx] = { type: "todo", id, todos, status: "running" as const };
          return updated;
        }
        return [...prev, { type: "todo", id, todos, status: "running" as const }];
      });
    } catch {
      // malformed input — ignore
    }
  }, [applyUpdate]);

  const handleAskQuestion = useCallback(
    (requestId: string, question: string, options: string[]) => {
      applyUpdate((prev) => [
        ...prev,
        { type: "ask_question", requestId, question, options: options.length ? options : undefined },
      ]);
    },
    [applyUpdate]
  );

  const handleDone = useCallback((messageId: string) => {
    setIsStreaming(false);
    const blocks = streamBlocksRef.current;
    const textContent = blocks
      .filter((b): b is Extract<StreamBlock, { type: "text" }> => b.type === "text")
      .map((b) => b.content)
      .join("");
    streamBlocksRef.current = [];
    setStreamBlocks([]);
    if (textContent && conversationId) {
      const msg: Message = {
        id: messageId || crypto.randomUUID(),
        conversation_id: conversationId,
        role: "assistant",
        content: textContent,
        created_at: new Date().toISOString(),
      };
      setMessages((prev) => [...prev, msg]);
    }
  }, [conversationId]);

  const handleError = useCallback((errMsg: string) => {
    setIsStreaming(false);
    streamBlocksRef.current = [];
    setStreamBlocks([]);
    console.error("Agent error:", errMsg);
  }, []);

  const handleConversationId = useCallback(() => {}, []);

  const { sendChat, sendQuestionReply, status, connected } = useWebSocket({
    onToken: handleToken,
    onDone: handleDone,
    onError: handleError,
    onConversationId: handleConversationId,
    onThinkingDelta: handleThinkingDelta,
    onToolUse: handleToolUse,
    onToolResult: handleToolResult,
    onTodoUpdate: handleTodoUpdate,
    onAskQuestion: handleAskQuestion,
  });

  const handleAnswerQuestion = useCallback(
    (requestId: string, answer: string) => {
      sendQuestionReply(requestId, answer);
      applyUpdate((prev) =>
        prev.map((b) =>
          b.type === "ask_question" && b.requestId === requestId
            ? { ...b, answered: answer }
            : b
        )
      );
    },
    [sendQuestionReply, applyUpdate]
  );

  const handleSend = useCallback(
    (content: string) => {
      if (!conversationId) return;
      const userMsg: Message = {
        id: crypto.randomUUID(),
        conversation_id: conversationId,
        role: "user",
        content,
        created_at: new Date().toISOString(),
      };
      setMessages((prev) => [...prev, userMsg]);
      streamBlocksRef.current = [];
      setStreamBlocks([]);
      setIsStreaming(true);
      sendChat(conversationId, content);
    },
    [conversationId, sendChat]
  );

  if (!conversationId) {
    return (
      <div className="flex-1 flex flex-col">
        {/* Header placeholder */}
        <header className="h-14 border-b border-slate-100 flex items-center px-6 bg-white/80 backdrop-blur-md shrink-0">
          <div className="flex items-center gap-3">
            <div className="w-8 h-8 rounded-lg bg-indigo-50 flex items-center justify-center">
              <MessageSquare size={16} className="text-indigo-600" />
            </div>
            <span className="font-semibold text-slate-400 text-sm">Agent Chat</span>
          </div>
          <span className="ml-auto text-xs text-slate-300">WALL-AI · uBuilding</span>
        </header>
        <div className="flex-1 flex items-center justify-center">
          <div className="text-center space-y-2">
            <p className="text-base text-slate-400">选择或创建一个对话</p>
            <p className="text-sm text-slate-300">点击左侧「新对话」开始</p>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="flex-1 flex flex-col min-h-0">
      {/* Header */}
      <header className="h-14 border-b border-slate-100 flex items-center px-6 bg-white/80 backdrop-blur-md shrink-0 z-10">
        <div className="flex items-center gap-3">
          <div className="w-8 h-8 rounded-lg bg-indigo-50 flex items-center justify-center">
            <MessageSquare size={16} className="text-indigo-600" />
          </div>
          <span className="font-semibold text-slate-800 text-sm truncate max-w-xs">
            {title ?? "Agent Chat"}
          </span>
        </div>
        <div className="ml-auto flex items-center gap-3">
          <span className="flex items-center gap-1.5 text-xs text-slate-400">
            <span
              className={`w-1.5 h-1.5 rounded-full ${
                connected ? "bg-emerald-500" : "bg-amber-400 animate-pulse"
              }`}
            />
            {connected ? "已连接" : "连接中..."}
          </span>
          <span className="text-xs text-slate-300">WALL-AI · uBuilding</span>
        </div>
      </header>

      <MessageList
        messages={messages}
        streamBlocks={streamBlocks}
        isStreaming={isStreaming}
        onAnswerQuestion={handleAnswerQuestion}
      />
      <InputBar
        onSend={handleSend}
        status={status}
        connected={connected}
        disabled={!conversationId}
      />
    </div>
  );
}

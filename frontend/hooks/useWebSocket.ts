"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import type { WSServerMessage, SendStatus } from "@/types/chat";

interface UseWebSocketOptions {
  onToken: (text: string) => void;
  onDone: (messageId: string) => void;
  onError: (msg: string) => void;
  onConversationId: (id: string) => void;
  onThinkingDelta?: (text: string) => void;
  onToolUse?: (id: string, name: string, input: string) => void;
  onToolResult?: (toolId: string, content: string) => void;
}

export function useWebSocket(opts: UseWebSocketOptions) {
  const wsRef = useRef<WebSocket | null>(null);
  const [status, setStatus] = useState<SendStatus>("idle");
  const [connected, setConnected] = useState(false);
  const optsRef = useRef(opts);
  optsRef.current = opts;

  const connect = useCallback(() => {
    const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    const backendUrl = process.env.NEXT_PUBLIC_BACKEND_URL || "http://localhost:8080";
    const backendHost = new URL(backendUrl).host;
    const url = `${protocol}//${backendHost}/ws`;

    const ws = new WebSocket(url);
    wsRef.current = ws;

    ws.onopen = () => setConnected(true);
    ws.onclose = () => {
      setConnected(false);
      setTimeout(connect, 3000);
    };
    ws.onerror = () => {
      setConnected(false);
    };
    ws.onmessage = (e) => {
      const msg: WSServerMessage = JSON.parse(e.data);
      switch (msg.type) {
        case "token":
          optsRef.current.onToken(msg.content ?? "");
          break;
        case "done":
          setStatus("idle");
          optsRef.current.onDone(msg.message_id ?? "");
          break;
        case "error":
          setStatus("idle");
          optsRef.current.onError(msg.content ?? "Unknown error");
          break;
        case "conversation_id":
          optsRef.current.onConversationId(msg.conversation_id ?? "");
          break;
        case "thinking_delta":
          optsRef.current.onThinkingDelta?.(msg.content ?? "");
          break;
        case "tool_use":
          optsRef.current.onToolUse?.(msg.tool_id ?? "", msg.tool_name ?? "", msg.content ?? "");
          break;
        case "tool_result":
          optsRef.current.onToolResult?.(msg.tool_id ?? "", msg.content ?? "");
          break;
      }
    };
  }, []);

  useEffect(() => {
    connect();
    return () => {
      wsRef.current?.close();
    };
  }, [connect]);

  const sendChat = useCallback((conversationId: string, content: string) => {
    if (!wsRef.current || wsRef.current.readyState !== WebSocket.OPEN) return;
    setStatus("streaming");
    wsRef.current.send(
      JSON.stringify({ type: "chat", conversation_id: conversationId, content })
    );
  }, []);

  const requestNewConversation = useCallback(() => {
    if (!wsRef.current || wsRef.current.readyState !== WebSocket.OPEN) return;
    wsRef.current.send(JSON.stringify({ type: "new_conversation" }));
  }, []);

  return { sendChat, requestNewConversation, status, connected };
}

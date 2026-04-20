"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import MessageList from "./MessageList";
import InputBar from "./InputBar";
import { useWebSocket } from "@/hooks/useWebSocket";
import { fetchMessages } from "@/utils/api";
import type { Message, StreamBlock } from "@/types/chat";

interface ChatDialogProps {
  conversationId: string | null;
  onTitleUpdated?: (id: string, title: string) => void;
}

export default function ChatDialog({ conversationId, onTitleUpdated }: ChatDialogProps) {
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

  const { sendChat, status, connected } = useWebSocket({
    onToken: handleToken,
    onDone: handleDone,
    onError: handleError,
    onConversationId: handleConversationId,
    onThinkingDelta: handleThinkingDelta,
    onToolUse: handleToolUse,
    onToolResult: handleToolResult,
  });

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
      <div className="flex-1 flex items-center justify-center text-gray-600">
        <div className="text-center">
          <p className="text-lg">选择或创建一个对话</p>
          <p className="text-sm mt-1 text-gray-700">点击左侧「新对话」开始</p>
        </div>
      </div>
    );
  }

  return (
    <div className="flex-1 flex flex-col min-h-0">
      <MessageList
        messages={messages}
        streamBlocks={streamBlocks}
        isStreaming={isStreaming}
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

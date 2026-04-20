"use client";

import { useCallback, useState } from "react";
import Sidebar from "@/components/Sidebar";
import ChatDialog from "@/components/ChatDialog";
import { useConversations } from "@/hooks/useConversations";
import { createConversation } from "@/utils/api";
import type { Conversation } from "@/types/chat";

export default function ChatPage() {
  const {
    conversations,
    addConversation,
    removeConversation,
    renameConversation,
    updateTitle,
  } = useConversations();

  const [activeId, setActiveId] = useState<string | null>(null);

  const handleNew = useCallback(async () => {
    try {
      const conv: Conversation = await createConversation();
      addConversation(conv);
      setActiveId(conv.id);
    } catch (e) {
      console.error("Failed to create conversation:", e);
    }
  }, [addConversation]);

  const handleSelect = useCallback((id: string) => {
    setActiveId(id);
  }, []);

  const handleDelete = useCallback(
    async (id: string) => {
      await removeConversation(id);
      if (activeId === id) setActiveId(null);
    },
    [activeId, removeConversation]
  );

  const handleRename = useCallback(
    (id: string, title: string) => {
      renameConversation(id, title);
    },
    [renameConversation]
  );

  const handleTitleUpdated = useCallback(
    (id: string, title: string) => {
      updateTitle(id, title);
    },
    [updateTitle]
  );

  const activeTitle = conversations.find((c) => c.id === activeId)?.title ?? null;

  return (
    <div className="flex h-full">
      <Sidebar
        conversations={conversations}
        activeId={activeId}
        onSelect={handleSelect}
        onNew={handleNew}
        onDelete={handleDelete}
        onRename={handleRename}
      />
      <main className="flex-1 flex flex-col min-w-0 bg-white overflow-hidden">
        <ChatDialog
          conversationId={activeId}
          title={activeTitle}
          onTitleUpdated={handleTitleUpdated}
        />
      </main>
    </div>
  );
}

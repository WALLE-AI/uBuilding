"use client";

import { useState, useRef, useCallback, useEffect } from "react";
import {
  MessageSquare, Plus, Trash2, Edit2, Check, X,
  Clock, PanelLeft, Search, ChevronDown,
  Cpu, Compass, Wrench, Database, BookOpen, Layers, Folder,
} from "lucide-react";
import type { Conversation } from "@/types/chat";
import WorkspaceModal from "@/components/WorkspaceModal";
import { getWorkspace } from "@/utils/api";

interface SidebarProps {
  conversations: Conversation[];
  activeId: string | null;
  onSelect: (id: string) => void;
  onNew: () => void;
  onDelete: (id: string) => void;
  onRename: (id: string, title: string) => void;
}

function formatTime(dateStr: string): string {
  const normalized =
    dateStr && !dateStr.endsWith("Z") && !dateStr.includes("+")
      ? dateStr + "Z"
      : dateStr;
  const date = new Date(normalized);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMins = Math.floor(diffMs / 60000);
  const diffHours = Math.floor(diffMs / 3600000);
  const diffDays = Math.floor(diffMs / 86400000);

  if (diffMins < 1) return "刚刚";
  if (diffMins < 60) return `${diffMins}分钟前`;
  if (diffHours < 24) return `${diffHours}小时前`;
  if (diffDays < 7) return `${diffDays}天前`;
  return date.toLocaleDateString("zh-CN");
}

export default function Sidebar({
  conversations,
  activeId,
  onSelect,
  onNew,
  onDelete,
  onRename,
}: SidebarProps) {
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editTitle, setEditTitle] = useState("");

  const [isCollapsed, setIsCollapsed] = useState(false);
  const [width, setWidth] = useState(256);
  const [isResizing, setIsResizing] = useState(false);
  const [currentWorkspace, setCurrentWorkspace] = useState("");
  const [showWorkspaceModal, setShowWorkspaceModal] = useState(false);
  const [isAbilityCenterExpanded, setIsAbilityCenterExpanded] = useState(true);
  const [isArchivesExpanded, setIsArchivesExpanded] = useState(true);
  const [isWorkspacesExpanded, setIsWorkspacesExpanded] = useState(true);
  const [isRecentExpanded, setIsRecentExpanded] = useState(true);
  const [showRecentPopup, setShowRecentPopup] = useState(false);
  const timeoutRef = useRef<number | null>(null);

  const startEdit = (conv: Conversation) => {
    setEditingId(conv.id);
    setEditTitle(conv.title);
  };

  const commitEdit = () => {
    if (editingId && editTitle.trim()) {
      onRename(editingId, editTitle.trim());
    }
    setEditingId(null);
  };

  const cancelEdit = () => setEditingId(null);

  const startResizing = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    setIsResizing(true);
  }, []);

  const stopResizing = useCallback(() => {
    setIsResizing(false);
  }, []);

  const resize = useCallback(
    (e: MouseEvent) => {
      if (isResizing) {
        let newWidth = e.clientX;
        if (newWidth < 160) newWidth = 160;
        if (newWidth > 480) newWidth = 480;
        setWidth(newWidth);
      }
    },
    [isResizing]
  );

  useEffect(() => {
    if (isResizing) {
      window.addEventListener("mousemove", resize);
      window.addEventListener("mouseup", stopResizing);
    } else {
      window.removeEventListener("mousemove", resize);
      window.removeEventListener("mouseup", stopResizing);
    }
    return () => {
      window.removeEventListener("mousemove", resize);
      window.removeEventListener("mouseup", stopResizing);
    };
  }, [isResizing, resize, stopResizing]);

  useEffect(() => {
    getWorkspace().then(setCurrentWorkspace).catch(() => {});
  }, []);

  const handleMouseEnter = () => {
    if (timeoutRef.current) window.clearTimeout(timeoutRef.current);
    setShowRecentPopup(true);
  };

  const handleMouseLeave = () => {
    timeoutRef.current = window.setTimeout(() => {
      setShowRecentPopup(false);
    }, 300);
  };

  return (
    <aside
      style={{ width: isCollapsed ? "64px" : `${width}px` }}
      className={`relative flex flex-col bg-slate-50 border-r border-slate-200 h-full flex-shrink-0 ${
        isResizing ? "cursor-col-resize" : "transition-all duration-300 ease-in-out"
      }`}
    >
      {/* Resize handle */}
      {!isCollapsed && (
        <div
          onMouseDown={startResizing}
          className="absolute top-0 right-[-2px] w-1 h-full cursor-col-resize z-50 hover:bg-indigo-400/30 transition-colors"
        />
      )}

      {/* Header: Brand + Search + Toggle */}
      <div
        className={`h-14 px-3 flex items-center border-b border-slate-200 shrink-0 ${
          isCollapsed ? "justify-center" : "justify-between"
        }`}
      >
        {!isCollapsed && (
          <div className="flex items-center gap-2.5">
            <div className="w-7 h-7 rounded-lg bg-indigo-600 flex items-center justify-center shadow-sm shadow-indigo-500/30 shrink-0">
              <MessageSquare size={14} className="text-white" />
            </div>
            <span className="font-semibold text-slate-800 text-sm whitespace-nowrap">Agent Chat</span>
          </div>
        )}
        <div className="flex items-center gap-1">
          {!isCollapsed && (
            <button className="p-1.5 rounded-lg text-slate-400 hover:text-slate-600 hover:bg-slate-200/60 transition-colors">
              <Search size={15} />
            </button>
          )}
          <button
            onClick={() => setIsCollapsed(!isCollapsed)}
            className="p-1.5 rounded-lg text-slate-500 hover:text-slate-700 hover:bg-slate-200/60 transition-colors"
            title={isCollapsed ? "展开侧边栏" : "收起侧边栏"}
          >
            <PanelLeft size={16} />
          </button>
        </div>
      </div>

      {/* New conversation button */}
      <div className={`px-3 pt-3 pb-2 ${isCollapsed ? "flex justify-center" : ""}`}>
        {isCollapsed ? (
          <button
            onClick={onNew}
            title="新对话"
            className="w-10 h-10 flex items-center justify-center rounded-xl bg-indigo-600 hover:bg-indigo-500 active:scale-[0.97] text-white transition-all shadow-sm shadow-indigo-500/20"
          >
            <Plus size={18} />
          </button>
        ) : (
          <button
            onClick={onNew}
            className="w-full flex items-center justify-center gap-2 px-3 py-2.5 rounded-xl bg-indigo-600 hover:bg-indigo-500 active:scale-[0.98] text-white text-sm font-medium transition-all shadow-sm shadow-indigo-500/20"
          >
            <Plus size={15} />
            新对话
          </button>
        )}
      </div>

      {/* Nav sections */}
      <nav className="flex-1 overflow-y-auto px-2 pb-2">
        {isCollapsed ? (
          /* ── Collapsed: icon strip ── */
          <div className="flex flex-col items-center gap-0.5 pt-1">
            {/* 技能中心 */}
            <button
              className="w-10 h-10 flex items-center justify-center rounded-xl text-slate-500 hover:bg-slate-200/60 transition-all"
              title="技能中心"
            >
              <Cpu size={18} />
            </button>

            {/* 资料库 */}
            <button
              className="w-10 h-10 flex items-center justify-center rounded-xl text-slate-500 hover:bg-slate-200/60 transition-all"
              title="资料库"
            >
              <Database size={18} />
            </button>

            {/* 工作空间 */}
            <button
              className="w-10 h-10 flex items-center justify-center rounded-xl text-slate-500 hover:bg-slate-200/60 transition-all"
              title="工作空间"
            >
              <Layers size={18} />
            </button>

            {/* 最近 */}
            <div
              className="relative"
              onMouseEnter={handleMouseEnter}
              onMouseLeave={handleMouseLeave}
            >
              <button
                className={`w-10 h-10 flex items-center justify-center rounded-xl transition-all ${
                  showRecentPopup
                    ? "bg-slate-200/80 text-indigo-600"
                    : "text-slate-500 hover:bg-slate-200/60"
                }`}
                title="最近"
              >
                <Clock size={18} />
              </button>

              {showRecentPopup && (
                <div
                  className="absolute left-[calc(100%+8px)] top-0 w-72 bg-white border border-slate-200 shadow-2xl rounded-2xl p-4 z-[100]"
                  onMouseEnter={handleMouseEnter}
                  onMouseLeave={handleMouseLeave}
                >
                  <div className="mb-3 px-1">
                    <span className="text-sm font-bold text-slate-800">最近</span>
                  </div>
                  <div className="space-y-0.5">
                    {conversations.length === 0 ? (
                      <p className="text-slate-400 text-xs px-2 py-1 italic">暂无对话记录</p>
                    ) : (
                      conversations.map((conv) => (
                        <button
                          key={conv.id}
                          onClick={() => { onSelect(conv.id); setShowRecentPopup(false); }}
                          className={`w-full flex items-center gap-2.5 px-3 py-2 rounded-xl text-[13px] text-left transition-all ${
                            activeId === conv.id
                              ? "bg-indigo-50 text-indigo-700"
                              : "text-slate-600 hover:bg-slate-50"
                          }`}
                        >
                          <MessageSquare
                            size={13}
                            className={`flex-shrink-0 ${activeId === conv.id ? "text-indigo-500" : "text-slate-400"}`}
                          />
                          <span className="truncate flex-1">{conv.title || "未命名对话"}</span>
                        </button>
                      ))
                    )}
                  </div>
                </div>
              )}
            </div>
          </div>
        ) : (
          /* ── Expanded: all sections ── */
          <div className="space-y-4 pt-1">

            {/* ── 技能中心 ── */}
            <div>
              <button
                onClick={() => setIsAbilityCenterExpanded(!isAbilityCenterExpanded)}
                className="w-full flex items-center justify-between px-3 py-2 text-xs font-semibold text-slate-400 uppercase tracking-widest hover:text-slate-600 transition-colors"
              >
                <div className="flex items-center gap-2">
                  <Cpu size={13} />
                  <span>技能中心</span>
                </div>
                <ChevronDown
                  size={13}
                  className={`transition-transform duration-200 ${isAbilityCenterExpanded ? "" : "-rotate-90"}`}
                />
              </button>
              <div
                className={`overflow-hidden transition-all duration-300 ease-in-out ${
                  isAbilityCenterExpanded ? "max-h-[200px] opacity-100" : "max-h-0 opacity-0"
                }`}
              >
                <button className="w-full flex items-center gap-2 pl-7 pr-3 py-2 rounded-xl text-[13px] text-slate-500 hover:bg-slate-200/60 transition-all">
                  <Compass size={13} className="shrink-0" />
                  <span>技能中心</span>
                </button>
                <button className="w-full flex items-center gap-2 pl-7 pr-3 py-2 rounded-xl text-[13px] text-slate-500 hover:bg-slate-200/60 transition-all">
                  <Wrench size={13} className="shrink-0" />
                  <span>工具中心</span>
                </button>
              </div>
            </div>

            {/* ── 资料库 ── */}
            <div>
              <button
                onClick={() => setIsArchivesExpanded(!isArchivesExpanded)}
                className="w-full flex items-center justify-between px-3 py-2 text-xs font-semibold text-slate-400 uppercase tracking-widest hover:text-slate-600 transition-colors"
              >
                <span>资料库</span>
                <ChevronDown
                  size={13}
                  className={`transition-transform duration-200 ${isArchivesExpanded ? "" : "-rotate-90"}`}
                />
              </button>
              <div
                className={`overflow-hidden transition-all duration-300 ease-in-out ${
                  isArchivesExpanded ? "max-h-[200px] opacity-100" : "max-h-0 opacity-0"
                }`}
              >
                <button className="w-full flex items-center gap-2 pl-7 pr-3 py-2 rounded-xl text-[13px] text-slate-500 hover:bg-slate-200/60 transition-all">
                  <Database size={13} className="shrink-0" />
                  <span>会话文档</span>
                </button>
                <button className="w-full flex items-center gap-2 pl-7 pr-3 py-2 rounded-xl text-[13px] text-slate-500 hover:bg-slate-200/60 transition-all">
                  <BookOpen size={13} className="shrink-0" />
                  <span>知识库</span>
                </button>
              </div>
            </div>

            {/* ── 工作空间 ── */}
            <div>
              <button
                onClick={() => setIsWorkspacesExpanded(!isWorkspacesExpanded)}
                className="w-full flex items-center justify-between px-3 py-2 text-xs font-semibold text-slate-400 uppercase tracking-widest hover:text-slate-600 transition-colors"
              >
                <span>工作空间</span>
                <ChevronDown
                  size={13}
                  className={`transition-transform duration-200 ${isWorkspacesExpanded ? "" : "-rotate-90"}`}
                />
              </button>
              <div
                className={`overflow-hidden transition-all duration-300 ease-in-out ${
                  isWorkspacesExpanded ? "max-h-[300px] opacity-100" : "max-h-0 opacity-0"
                }`}
              >
                <div className="pl-5 pr-1">
                  {currentWorkspace ? (
                    <div className="flex items-center gap-2 px-3 py-2 rounded-xl text-[13px] text-slate-600">
                      <div className="w-1.5 h-1.5 rounded-full bg-emerald-400 shrink-0" />
                      <Folder size={13} className="shrink-0 text-slate-400" />
                      <span className="truncate flex-1 text-left" title={currentWorkspace}>
                        {currentWorkspace.replace(/\\/g, "/").split("/").filter(Boolean).pop() ?? currentWorkspace}
                      </span>
                    </div>
                  ) : (
                    <p className="px-3 py-2 text-slate-400 text-xs italic">暂无工作空间</p>
                  )}
                  <button
                    onClick={() => setShowWorkspaceModal(true)}
                    className="w-full flex items-center gap-2 pl-4 pr-3 py-2 rounded-xl text-[13px] text-slate-400 hover:text-slate-600 hover:bg-slate-200/60 transition-all opacity-70 hover:opacity-100"
                  >
                    <Folder size={13} className="shrink-0" />
                    <span>设置工作空间</span>
                  </button>
                </div>
              </div>
            </div>

            {/* ── 最近 ── */}
            <div>
              <button
                onClick={() => setIsRecentExpanded(!isRecentExpanded)}
                className="w-full flex items-center justify-between px-3 py-2 text-xs font-semibold text-slate-400 uppercase tracking-widest hover:text-slate-600 transition-colors"
              >
                <span>最近</span>
                <ChevronDown
                  size={13}
                  className={`transition-transform duration-200 ${isRecentExpanded ? "" : "-rotate-90"}`}
                />
              </button>
              <div
                className={`overflow-hidden transition-all duration-300 ease-in-out ${
                  isRecentExpanded ? "max-h-[9999px] opacity-100" : "max-h-0 opacity-0"
                }`}
              >
                {conversations.length === 0 ? (
                  <p className="text-slate-400 text-xs text-center mt-4 px-4 leading-relaxed">
                    暂无对话记录<br />点击「新对话」开始
                  </p>
                ) : (
                  <div className="space-y-0.5 mt-0.5">
                    {conversations.map((conv) => (
                      <div
                        key={conv.id}
                        className={`group relative flex items-center gap-2.5 rounded-xl px-3 py-2.5 cursor-pointer transition-all ${
                          activeId === conv.id
                            ? "bg-white shadow-sm border border-slate-200 text-slate-800"
                            : "text-slate-500 hover:bg-white hover:text-slate-700 hover:shadow-sm hover:border hover:border-slate-200"
                        }`}
                        onClick={() => onSelect(conv.id)}
                      >
                        <MessageSquare
                          size={13}
                          className={`flex-shrink-0 ${activeId === conv.id ? "text-indigo-500" : "text-slate-400"}`}
                        />

                        {editingId === conv.id ? (
                          <div
                            className="flex-1 flex items-center gap-1"
                            onClick={(e) => e.stopPropagation()}
                          >
                            <input
                              autoFocus
                              value={editTitle}
                              onChange={(e) => setEditTitle(e.target.value)}
                              onKeyDown={(e) => {
                                if (e.key === "Enter") commitEdit();
                                if (e.key === "Escape") cancelEdit();
                              }}
                              className="flex-1 bg-slate-100 border border-slate-300 text-slate-800 text-sm rounded-lg px-2 py-0.5 outline-none focus:border-indigo-300 min-w-0 transition-colors"
                            />
                            <button
                              onClick={commitEdit}
                              className="text-emerald-500 hover:text-emerald-600 transition-colors"
                            >
                              <Check size={12} />
                            </button>
                            <button
                              onClick={cancelEdit}
                              className="text-slate-400 hover:text-slate-600 transition-colors"
                            >
                              <X size={12} />
                            </button>
                          </div>
                        ) : (
                          <>
                            <span className="flex-1 text-sm truncate min-w-0">
                              {conv.title || "未命名对话"}
                            </span>
                            <span className="text-[10px] text-slate-400 shrink-0 opacity-0 group-hover:opacity-100 transition-opacity mr-0.5">
                              {formatTime(conv.updated_at)}
                            </span>
                            <div className="hidden group-hover:flex items-center gap-0.5 shrink-0">
                              <button
                                onClick={(e) => { e.stopPropagation(); startEdit(conv); }}
                                className="p-1 rounded-lg text-slate-400 hover:text-slate-600 hover:bg-slate-100 transition-colors"
                              >
                                <Edit2 size={11} />
                              </button>
                              <button
                                onClick={(e) => { e.stopPropagation(); onDelete(conv.id); }}
                                className="p-1 rounded-lg text-slate-400 hover:text-red-500 hover:bg-red-50 transition-colors"
                              >
                                <Trash2 size={11} />
                              </button>
                            </div>
                          </>
                        )}
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </div>

          </div>
        )}
      </nav>

      {/* Footer */}
      <div className="px-4 py-3 border-t border-slate-200 shrink-0">
        {isCollapsed ? (
          <div className="flex justify-center">
            <div className="w-1.5 h-1.5 rounded-full bg-slate-300" />
          </div>
        ) : (
          <p className="text-xs text-slate-400 text-center">WALL-AI · uBuilding v0.1</p>
        )}
      </div>
      <WorkspaceModal
        isOpen={showWorkspaceModal}
        onClose={() => setShowWorkspaceModal(false)}
        onSuccess={(path) => setCurrentWorkspace(path)}
      />
    </aside>
  );
}

"use client";

import { useState, useEffect } from "react";
import { X, FolderOpen, Loader2, CheckCircle2, AlertCircle } from "lucide-react";
import { getWorkspace, setWorkspace } from "@/utils/api";

interface WorkspaceModalProps {
  isOpen: boolean;
  onClose: () => void;
  onSuccess: (path: string) => void;
}

export default function WorkspaceModal({ isOpen, onClose, onSuccess }: WorkspaceModalProps) {
  const [pathInput, setPathInput] = useState("");
  const [currentPath, setCurrentPath] = useState("");
  const [error, setError] = useState("");
  const [isLoading, setIsLoading] = useState(false);
  const [supportsFilePicker, setSupportsFilePicker] = useState(false);

  useEffect(() => {
    setSupportsFilePicker(typeof window !== "undefined" && "showDirectoryPicker" in window);
  }, []);

  useEffect(() => {
    if (!isOpen) return;
    setError("");
    getWorkspace()
      .then((path) => {
        setCurrentPath(path);
        setPathInput(path);
      })
      .catch(() => {});
  }, [isOpen]);

  const handleBrowse = async () => {
    try {
      const dir = await (window as any).showDirectoryPicker({ mode: "read" });
      setPathInput((prev) => {
        const trimmed = prev.trim().replace(/[\\/]+$/, "");
        if (trimmed.endsWith(dir.name) || trimmed.endsWith(`/${dir.name}`) || trimmed.endsWith(`\\${dir.name}`)) {
          return prev;
        }
        if (!trimmed) return dir.name;
        return trimmed + "\\" + dir.name;
      });
      setError("");
    } catch {
    }
  };

  const handleConfirm = async () => {
    const trimmed = pathInput.trim();
    if (!trimmed) {
      setError("路径不能为空");
      return;
    }
    setIsLoading(true);
    setError("");
    try {
      const newPath = await setWorkspace(trimmed);
      onSuccess(newPath);
      onClose();
    } catch (e: any) {
      setError(e.message ?? "设置失败，请检查路径是否存在");
    } finally {
      setIsLoading(false);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter") handleConfirm();
    if (e.key === "Escape") onClose();
  };

  if (!isOpen) return null;

  const lastSegment = (p: string) => {
    const parts = p.replace(/\\/g, "/").split("/").filter(Boolean);
    return parts[parts.length - 1] ?? p;
  };

  return (
    <div
      className="fixed inset-0 bg-black/50 flex items-center justify-center z-[200]"
      onClick={(e) => { if (e.target === e.currentTarget) onClose(); }}
    >
      <div className="bg-white rounded-2xl shadow-2xl w-full max-w-md mx-4 overflow-hidden">
        {/* Header */}
        <div className="flex items-center justify-between px-6 pt-5 pb-4 border-b border-slate-100">
          <div className="flex items-center gap-2.5">
            <div className="w-8 h-8 rounded-lg bg-indigo-50 flex items-center justify-center">
              <FolderOpen size={16} className="text-indigo-600" />
            </div>
            <h2 className="text-base font-semibold text-slate-800">设置工作空间</h2>
          </div>
          <button
            onClick={onClose}
            className="p-1.5 rounded-lg text-slate-400 hover:text-slate-600 hover:bg-slate-100 transition-colors"
          >
            <X size={16} />
          </button>
        </div>

        <div className="px-6 py-5 space-y-4">
          {/* Current workspace */}
          {currentPath && (
            <div className="flex items-start gap-2 px-3 py-2.5 bg-slate-50 rounded-xl border border-slate-200">
              <CheckCircle2 size={14} className="text-emerald-500 mt-0.5 shrink-0" />
              <div className="min-w-0">
                <p className="text-[11px] text-slate-400 mb-0.5">当前工作空间</p>
                <p className="text-[13px] text-slate-600 truncate" title={currentPath}>
                  <span className="text-slate-400">{currentPath.slice(0, currentPath.lastIndexOf(lastSegment(currentPath)))}</span>
                  <span className="font-medium text-slate-700">{lastSegment(currentPath)}</span>
                </p>
              </div>
            </div>
          )}

          {/* Path input */}
          <div className="space-y-1.5">
            <label className="text-xs font-medium text-slate-600">新工作空间路径</label>
            <div className="flex gap-2">
              <input
                autoFocus
                type="text"
                value={pathInput}
                onChange={(e) => { setPathInput(e.target.value); setError(""); }}
                onKeyDown={handleKeyDown}
                placeholder="C:\Users\...\my-project"
                className="flex-1 border border-slate-300 rounded-xl px-3 py-2 text-sm text-slate-800 outline-none focus:border-indigo-400 focus:ring-2 focus:ring-indigo-100 transition-all placeholder:text-slate-300 min-w-0"
              />
              {supportsFilePicker && (
                <button
                  onClick={handleBrowse}
                  title="浏览目录（仅显示目录名，需手动补全完整路径）"
                  className="flex items-center gap-1.5 px-3 py-2 rounded-xl bg-slate-100 hover:bg-slate-200 text-sm text-slate-600 transition-colors shrink-0"
                >
                  <FolderOpen size={14} />
                  <span>浏览</span>
                </button>
              )}
            </div>
            {supportsFilePicker && (
              <p className="text-[11px] text-slate-400">
                浏览按钮仅回填目录名，请手动补全完整绝对路径后确认
              </p>
            )}
          </div>

          {/* Error banner */}
          {error && (
            <div className="flex items-start gap-2 px-3 py-2.5 bg-red-50 border border-red-200 rounded-xl">
              <AlertCircle size={14} className="text-red-500 mt-0.5 shrink-0" />
              <p className="text-sm text-red-600">{error}</p>
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-end gap-2 px-6 pb-5">
          <button
            onClick={onClose}
            className="px-4 py-2 rounded-xl text-sm text-slate-600 hover:bg-slate-100 transition-colors"
          >
            取消
          </button>
          <button
            onClick={handleConfirm}
            disabled={isLoading}
            className="flex items-center gap-2 px-5 py-2 rounded-xl bg-indigo-600 hover:bg-indigo-500 active:scale-[0.98] text-white text-sm font-medium transition-all shadow-sm shadow-indigo-500/20 disabled:opacity-60 disabled:cursor-not-allowed"
          >
            {isLoading ? (
              <>
                <Loader2 size={14} className="animate-spin" />
                <span>确认中...</span>
              </>
            ) : (
              <span>确认</span>
            )}
          </button>
        </div>
      </div>
    </div>
  );
}

import { useState } from 'react'
import { Code2, Eraser, FileText, FolderOpen, MoreHorizontal, Pencil, Power, SquareTerminal, Trash2 } from 'lucide-react'
import type { ServerView } from '../../api/types'
import { useT } from '../../i18n'
import { useRemote } from '../../state/live'
import { openDialog } from '../../state/ui'
import { Menu, MenuItem, MenuSeparator } from '../../ui/Menu'
import { ConfirmDialog } from '../dialogs/ConfirmDialog'
import { clearScrollback, openOnDesktop, removeServer, removeTemporary, setEnabled } from './actions'

/** Overflow actions for one server: open it on the desktop, edit, disable, clear its output, remove. */
export function ServerMenu({ server: v, className = 'icon-btn' }: { server: ServerView; className?: string }) {
  const t = useT()
  const [confirm, setConfirm] = useState<'remove' | 'clear' | null>(null)
  // Desktop actions would act on the other computer's screen.
  const local = !useRemote()
  return (
    <>
      <Menu label={t('detail.more')} trigger={<MoreHorizontal size={18} />} className={className}>
        {local && (
          <>
            <MenuItem icon={<FolderOpen size={16} />} onSelect={() => openOnDesktop('folder', v.name)}>{t('menu.openFolder')}</MenuItem>
            <MenuItem icon={<Code2 size={16} />} onSelect={() => openOnDesktop('editor', v.name)}>{t('menu.openEditor')}</MenuItem>
            <MenuItem icon={<SquareTerminal size={16} />} onSelect={() => openOnDesktop('terminal', v.name)}>{t('menu.openTerminal')}</MenuItem>
            <MenuItem icon={<FileText size={16} />} onSelect={() => openOnDesktop('logs', v.name)}>{t('menu.openLogFile')}</MenuItem>
            <MenuSeparator />
          </>
        )}
        {!v.temporary && (
          <>
            <MenuItem icon={<Pencil size={16} />} onSelect={() => openDialog({ kind: 'server', mode: 'manual', edit: v.name })}>{t('menu.edit')}</MenuItem>
            <MenuItem icon={<Power size={16} />} onSelect={() => setEnabled(v.name, !v.enabled)}>{v.enabled ? t('menu.disable') : t('menu.enable')}</MenuItem>
            <MenuSeparator />
          </>
        )}
        <MenuItem icon={<Eraser size={16} />} onSelect={() => setConfirm('clear')}>{t('menu.clearScrollback')}</MenuItem>
        <MenuItem icon={<Trash2 size={16} />} danger onSelect={() => setConfirm('remove')}>{t('menu.remove')}</MenuItem>
      </Menu>
      {confirm === 'remove' && (
        <ConfirmDialog
          title={t('confirm.removeTitle', { name: v.name })}
          message={t(v.temporary ? 'confirm.removeTemporaryBody' : 'confirm.removeBody')}
          confirmLabel={t('menu.remove')}
          onConfirm={() => (v.temporary ? removeTemporary(v.name) : removeServer(v.name))}
          onClose={() => setConfirm(null)}
        />
      )}
      {confirm === 'clear' && (
        <ConfirmDialog
          title={t('confirm.clearTitle', { name: v.name })}
          message={t('confirm.clearBody')}
          confirmLabel={t('confirm.clear')}
          onConfirm={() => clearScrollback(v.name)}
          onClose={() => setConfirm(null)}
        />
      )}
    </>
  )
}

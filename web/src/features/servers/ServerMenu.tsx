import { useState } from 'react'
import { Code2, FolderOpen, MoreHorizontal, Pencil, Power, SquareTerminal, Trash2 } from 'lucide-react'
import type { ServerView } from '../../api/types'
import { useT } from '../../i18n'
import { openDialog } from '../../state/ui'
import { Menu, MenuItem, MenuSeparator } from '../../ui/Menu'
import { ConfirmDialog } from '../dialogs/ConfirmDialog'
import { openOnDesktop, removeServer, setEnabled } from './actions'

/** Overflow actions for one server: open it on the desktop, edit, disable, remove. */
export function ServerMenu({ server: v, className = 'icon-btn' }: { server: ServerView; className?: string }) {
  const t = useT()
  const [confirmRemove, setConfirmRemove] = useState(false)
  return (
    <>
      <Menu label={t('detail.more')} trigger={<MoreHorizontal size={18} />} className={className}>
        <MenuItem icon={<FolderOpen size={16} />} onSelect={() => openOnDesktop('folder', v.name)}>{t('menu.openFolder')}</MenuItem>
        <MenuItem icon={<Code2 size={16} />} onSelect={() => openOnDesktop('editor', v.name)}>{t('menu.openEditor')}</MenuItem>
        <MenuItem icon={<SquareTerminal size={16} />} onSelect={() => openOnDesktop('terminal', v.name)}>{t('menu.openTerminal')}</MenuItem>
        <MenuSeparator />
        <MenuItem icon={<Pencil size={16} />} onSelect={() => openDialog({ kind: 'server', mode: 'manual', edit: v.name })}>{t('menu.edit')}</MenuItem>
        <MenuItem icon={<Power size={16} />} onSelect={() => setEnabled(v.name, !v.enabled)}>{v.enabled ? t('menu.disable') : t('menu.enable')}</MenuItem>
        <MenuSeparator />
        <MenuItem icon={<Trash2 size={16} />} danger onSelect={() => setConfirmRemove(true)}>{t('menu.remove')}</MenuItem>
      </Menu>
      {confirmRemove && (
        <ConfirmDialog
          title={t('confirm.removeTitle', { name: v.name })}
          message={t('confirm.removeBody')}
          confirmLabel={t('menu.remove')}
          onConfirm={() => removeServer(v.name)}
          onClose={() => setConfirmRemove(false)}
        />
      )}
    </>
  )
}

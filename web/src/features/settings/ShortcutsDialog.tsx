import { useT } from '../../i18n'
import { closeDialog } from '../../state/ui'
import { Dialog } from '../../ui/Dialog'
import { SHORTCUTS } from '../../app/shortcuts'
import { keyLabel } from '../../ui/keys'
import './settings.css'


export function ShortcutsDialog() {
  const t = useT()
  return (
    <Dialog title={t('shortcuts.title')} size="md" onClose={closeDialog}>
      <div className="shortcuts">
        {SHORTCUTS.map((g) => (
          <section key={g.title}>
            <h3>{t(g.title)}</h3>
            <dl>
              {g.items.map((item) => (
                <div key={item.keys.join(' ')} className="shortcut">
                  <dt>
                    {item.keys.map((k, i) => (
                      <span key={k}>
                        {i > 0 && <span className="then">{item.chord ? t('shortcuts.then') : '+'}</span>}
                        <kbd>{keyLabel(k, t)}</kbd>
                      </span>
                    ))}
                  </dt>
                  <dd>{t(item.label)}</dd>
                </div>
              ))}
            </dl>
          </section>
        ))}
      </div>
    </Dialog>
  )
}

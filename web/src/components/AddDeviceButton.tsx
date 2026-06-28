import { useState } from 'react'
import { Plus } from 'lucide-react'
import { OnboardingWizard } from './OnboardingWizard'

// AddDeviceButton opens the registry-driven onboarding wizard pre-set to the current page's
// device type (page context). `defaultType` is the onboarding registry key (e.g. "bmc",
// "biometric_zkteco", "switch"); omit it for the All-pages where the operator picks the type.
export function AddDeviceButton({ defaultType, label }: { defaultType?: string; label: string }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button className="btn btn-primary btn-sm" onClick={() => setOpen(true)} style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
        <Plus size={14} /> {label}
      </button>
      {open && <OnboardingWizard defaultType={defaultType} onClose={() => setOpen(false)} />}
    </>
  )
}

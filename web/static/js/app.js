// Modals
function showModal(id){const el=document.getElementById(id);if(el){el.style.display='flex';document.body.style.overflow='hidden'}}
function hideModal(id){const el=document.getElementById(id);if(el){el.style.display='none';document.body.style.overflow=''}}
document.addEventListener('keydown',e=>{if(e.key==='Escape'){document.querySelectorAll('.modal').forEach(m=>m.style.display='none');document.body.style.overflow=''}})

// Tabs
function switchTab(btn,panelId){
  const container=btn.closest('.modal-body,.card-body,form')?.parentElement||btn.parentElement.parentElement
  btn.parentElement.querySelectorAll('.tab').forEach(t=>t.classList.remove('active'))
  container.querySelectorAll('.tab-panel').forEach(p=>{p.style.display='none';p.classList.remove('active')})
  btn.classList.add('active')
  const p=document.getElementById(panelId)
  if(p){p.style.display='block';p.classList.add('active')}
}

// Init first tab
document.addEventListener('DOMContentLoaded',()=>{
  document.querySelectorAll('.tabs').forEach(tabs=>{
    const first=tabs.querySelector('.tab')
    if(!first)return
    const m=first.getAttribute('onclick')?.match(/'([^']+)'\)/)
    if(m){
      const all=tabs.parentElement?.querySelectorAll('.tab-panel')||[]
      all.forEach(p=>p.style.display='none')
      const p=document.getElementById(m[1])
      if(p)p.style.display='block'
      first.classList.add('active')
    }
  })
})

// SSE install log
document.body.addEventListener('htmx:sseMessage',e=>{
  try{
    const d=JSON.parse(e.detail.data)
    const lines=document.getElementById('install-lines')
    if(!lines)return
    if(d.text){
      const span=document.createElement('span')
      span.className='log-line log-line-'+(d.level||'info')
      span.textContent=d.text+'\n'
      lines.appendChild(span)
      lines.scrollTop=lines.scrollHeight
    }
    if(d.done&&!d.error){toast('Модуль установлен!','ok');setTimeout(()=>location.reload(),1200)}
    if(d.done&&d.error){toast('Ошибка установки','error')}
  }catch(_){}
})

// Toast
function toast(msg,type='info',dur=3500){
  const c=document.getElementById('toast-container')
  if(!c)return
  const el=document.createElement('div')
  el.className='toast toast-'+type
  el.textContent=msg
  c.appendChild(el)
  setTimeout(()=>{el.style.opacity='0';el.style.transition='opacity .3s';setTimeout(()=>el.remove(),300)},dur)
}

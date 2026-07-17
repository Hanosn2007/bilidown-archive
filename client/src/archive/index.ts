import van, { State } from 'vanjs-core'
import { Route, goto } from 'vanjs-router'
import Danmaku from 'danmaku'
import { checkLogin, GLOBAL_HAS_LOGIN, ResJSON, VanComponent } from '../mixin'
import { LoadingBox } from '../view'

const { a, button, div, img, input, label, option, select, span, video } = van.tags

type ArchiveSettings = {
    enabled: boolean
    favMediaId: number
    intervalMinutes: number
    archiveFolder: string
    downloadAllPages: boolean
    downloadType: 'audio' | 'video' | 'merge'
    format: string
    preferredCodec: 12 | 7 | 13
    preferHiResAudio: boolean
}

type ArchiveItem = {
    id: number
    taskId: number
    bvid: string
    cid: number
    page: number
    title: string
    part: string
    owner: string
    filePath: string
    infoPath: string
    coverPath: string
    danmakuPath: string
    status: 'resolving' | 'waiting' | 'running' | 'done' | 'error' | 'unavailable'
    availability: 'unknown' | 'available' | 'unavailable' | 'restricted'
    message: string
    createdAt: string
    updatedAt: string
}

type ScanResult = {
    found: number
    created: number
    skipped: number
    failed: number
    messages: string[]
}

type ScanState = {
    id: string
    running: boolean
    result?: ScanResult
    error?: string
}

type BatchResult = {
    deleted: number
    retried: number
    skipped: number
    failed: number
    messages: string[]
}

const getSettings = async () => {
    const res = await fetch('/api/archive/getSettings').then(r => r.json()) as ResJSON<ArchiveSettings>
    if (!res.success) throw new Error(res.message)
    return res.data
}

const saveSettings = async (settings: ArchiveSettings) => {
    const res = await fetch('/api/archive/saveSettings', {
        method: 'POST',
        body: JSON.stringify(settings),
        headers: { 'Content-Type': 'application/json' }
    }).then(r => r.json()) as ResJSON
    if (!res.success) throw new Error(res.message)
}

const scanFavorite = async () => {
    const res = await fetch('/api/archive/scanFavorite', { method: 'POST' }).then(r => r.json()) as ResJSON<ScanState>
    if (!res.success) throw new Error(res.message)
    return res.data
}

const getScanStatus = async () => {
    const res = await fetch('/api/archive/scanStatus').then(r => r.json()) as ResJSON<ScanState>
    if (!res.success) throw new Error(res.message)
    return res.data
}

const listArchive = async (q = '') => {
    const res = await fetch(`/api/archive/list?q=${encodeURIComponent(q)}&limit=160`).then(r => r.json()) as ResJSON<ArchiveItem[]>
    if (!res.success) throw new Error(res.message)
    return res.data
}

const getInfo = async (path: string) => {
    const res = await fetch(`/api/downloadVideo?path=${encodeURIComponent(path)}`)
    if (!res.ok) throw new Error('读取归档信息失败')
    return res.json()
}

const deleteArchiveItems = async (ids: number[]) => {
    const res = await fetch('/api/archive/delete', {
        method: 'POST',
        body: JSON.stringify({ ids }),
        headers: { 'Content-Type': 'application/json' }
    }).then(r => r.json()) as ResJSON<BatchResult>
    if (!res.success) throw new Error(res.message)
    return res.data
}

const retryArchiveItems = async (ids: number[]) => {
    const res = await fetch('/api/archive/retry', {
        method: 'POST',
        body: JSON.stringify({ ids }),
        headers: { 'Content-Type': 'application/json' }
    }).then(r => r.json()) as ResJSON<BatchResult>
    if (!res.success) throw new Error(res.message)
    return res.data
}

const deleteArchivePreviews = async (ids: number[], all = false) => {
    const res = await fetch('/api/archive/deletePreview', {
        method: 'POST',
        body: JSON.stringify({ ids, all }),
        headers: { 'Content-Type': 'application/json' }
    }).then(r => r.json()) as ResJSON<BatchResult>
    if (!res.success) throw new Error(res.message)
    return res.data
}

const revealArchiveItem = async (id: number) => {
    const res = await fetch(`/api/archive/reveal?id=${id}`).then(r => r.json()) as ResJSON
    if (!res.success) throw new Error(res.message)
}

const parseBiliDanmakuXML = (xmlText: string) => {
    const doc = new DOMParser().parseFromString(xmlText, 'application/xml')
    return [...doc.querySelectorAll('d')].map(node => {
        const parts = (node.getAttribute('p') || '').split(',')
        const modeCode = Number(parts[1])
        const fontSize = Math.min(Math.max(Number(parts[2]) || 24, 16), 36)
        const color = Number(parts[3] || '16777215')
        const hexColor = `#${color.toString(16).padStart(6, '0')}`
        const mode = modeCode == 4 ? 'bottom' : modeCode == 5 ? 'top' : 'rtl'
        return {
            text: node.textContent || '',
            time: Number(parts[0]) || 0,
            mode,
            style: {
                font: `600 ${fontSize}px sans-serif`,
                fillStyle: hexColor,
                strokeStyle: 'rgba(0,0,0,.78)',
                lineWidth: 2,
                textBaseline: 'bottom',
            }
        }
    }).filter(comment => comment.text)
}

export class ArchiveRoute implements VanComponent {
    element: HTMLElement
    loading = van.state(true)
    saving = van.state(false)
    scanning = van.state(false)
    query = van.state('')
    statusFilter = van.state<'all' | ArchiveItem['status']>('all')
    ownerFilter = van.state('all')
    scanMessage = van.state('')
    operating = van.state(false)
    danmakuVisible = van.state(true)
    playerMessage = van.state('')
    playerFitMode = van.state<'contain' | 'cover' | 'fill'>('contain')
    settings: ArchiveSettings = {
        enabled: false,
        favMediaId: 0,
        intervalMinutes: 10,
        archiveFolder: '',
        downloadAllPages: true,
        downloadType: 'merge',
        format: 'highest',
        preferredCodec: 12,
        preferHiResAudio: true,
    }
    items: State<ArchiveItem[]> = van.state([])
    selectedIds: State<Set<number>> = van.state(new Set())
    activeItem: State<ArchiveItem | null> = van.state(null)
    activeInfo: State<any | null> = van.state(null)
    danmakuInstance: any = null
    danmakuResizeHandler: (() => void) | null = null
    playerResizeHandler: (() => void) | null = null
    archivePollTimer: number | null = null

    constructor() {
        this.element = this.Root()
    }

    Root() {
        const _that = this
        return Route({
            rule: 'archive',
            Loader() {
                return div(
                    () => _that.loading.val ? LoadingBox() : '',
                    () => _that.loading.val ? '' : div({ class: 'vstack gap-4' },
                        _that.SettingsPanel(),
                        _that.PlayerPanel(),
                        _that.ArchiveList(),
                    )
                )
            },
            async onFirst() {
                if (!await checkLogin()) return
            },
            async onLoad() {
                if (!GLOBAL_HAS_LOGIN.val) return goto('login')
                await _that.refresh()
                _that.startArchivePolling()
            }
        })
    }

    async refresh() {
        this.loading.val = true
        try {
            this.settings = await getSettings()
            this.items.val = await listArchive(this.query.val)
            this.selectedIds.val = new Set([...this.selectedIds.val].filter(id => this.items.val.some(item => item.id == id)))
        } catch (error) {
            alert((error as Error).message)
        } finally {
            this.loading.val = false
        }
    }

    SettingsPanel() {
        const enabled = van.state(this.settings.enabled)
        const favMediaId = van.state(this.settings.favMediaId.toString())
        const interval = van.state(this.settings.intervalMinutes.toString())
        const archiveFolder = van.state(this.settings.archiveFolder)
        const allPages = van.state(this.settings.downloadAllPages)
        const downloadType = van.state(this.settings.downloadType)
        const format = van.state(this.settings.format)
        const preferredCodec = van.state(this.settings.preferredCodec)
        const preferHiResAudio = van.state(this.settings.preferHiResAudio)

        van.derive(() => {
            enabled.val = this.settings.enabled
            favMediaId.val = this.settings.favMediaId.toString()
            interval.val = this.settings.intervalMinutes.toString()
            archiveFolder.val = this.settings.archiveFolder
            allPages.val = this.settings.downloadAllPages
            downloadType.val = this.settings.downloadType
            format.val = this.settings.format
            preferredCodec.val = this.settings.preferredCodec
            preferHiResAudio.val = this.settings.preferHiResAudio
        })

        return div({ class: 'vstack gap-3' },
            div({ class: 'h5 mb-0' }, '收藏夹监控'),
            div({ class: 'row g-2 align-items-end' },
                div({ class: 'col-12' },
                    label({ class: 'form-label' }, '归档备份目录'),
                    input({
                        class: 'form-control',
                        value: archiveFolder,
                        placeholder: '自动收藏夹备份保存到这里，普通下载不使用此目录',
                        oninput: (e) => archiveFolder.val = (e.target as HTMLInputElement).value
                    })
                )
            ),
            div({ class: 'row g-3 align-items-end' },
                div({ class: 'col-12 col-md-3' },
                    label({ class: 'form-label' }, '收藏夹 media_id'),
                    input({
                        class: 'form-control',
                        value: favMediaId,
                        placeholder: '例如 1234122612',
                        oninput: (e) => favMediaId.val = (e.target as HTMLInputElement).value
                    })
                ),
                div({ class: 'col-6 col-md-2' },
                    label({ class: 'form-label' }, '间隔(分钟)'),
                    input({
                        class: 'form-control',
                        type: 'number',
                        min: '1',
                        value: interval,
                        oninput: (e) => interval.val = (e.target as HTMLInputElement).value
                    })
                ),
                div({ class: 'col-6 col-md-2' },
                    label({ class: 'form-label' }, '清晰度'),
                    select({
                        class: 'form-select',
                        value: format,
                        oninput: (e) => format.val = (e.target as HTMLSelectElement).value
                    },
                        option({ value: 'highest' }, '最高可用'),
                        option({ value: '127' }, '8K'),
                        option({ value: '126' }, '杜比视界'),
                        option({ value: '125' }, 'HDR'),
                        option({ value: '120' }, '4K'),
                        option({ value: '116' }, '1080P60'),
                        option({ value: '112' }, '1080P+'),
                        option({ value: '80' }, '1080P')
                    )
                ),
                div({ class: 'col-6 col-md-2' },
                    label({ class: 'form-label' }, '优先编码'),
                    select({
                        class: 'form-select',
                        value: preferredCodec,
                        oninput: (e) => preferredCodec.val = Number((e.target as HTMLSelectElement).value) as ArchiveSettings['preferredCodec']
                    },
                        option({ value: '12' }, 'HEVC'),
                        option({ value: '7' }, 'AVC'),
                        option({ value: '13' }, 'AV1')
                    )
                ),
                div({ class: 'col-6 col-md-2' },
                    label({ class: 'form-label' }, '下载类型'),
                    select({
                        class: 'form-select',
                        value: downloadType,
                        oninput: (e) => downloadType.val = (e.target as HTMLSelectElement).value as ArchiveSettings['downloadType']
                    },
                        option({ value: 'merge' }, '音视频合并'),
                        option({ value: 'audio' }, '仅音频'),
                        option({ value: 'video' }, '仅视频')
                    )
                ),
                div({ class: 'col-6 col-md-2 vstack gap-2' },
                    label({ class: 'form-check' },
                        input({
                            class: 'form-check-input',
                            type: 'checkbox',
                            checked: preferHiResAudio,
                            oninput: (e) => preferHiResAudio.val = (e.target as HTMLInputElement).checked
                        }),
                        span({ class: 'form-check-label ms-2' }, 'Hi-Res')
                    ),
                    label({ class: 'form-check' },
                        input({
                            class: 'form-check-input',
                            type: 'checkbox',
                            checked: allPages,
                            oninput: (e) => allPages.val = (e.target as HTMLInputElement).checked
                        }),
                        span({ class: 'form-check-label ms-2' }, '下载全部分P')
                    )
                ),
                div({ class: 'col-6 col-md-2 vstack gap-2' },
                    label({ class: 'form-check' },
                        input({
                            class: 'form-check-input',
                            type: 'checkbox',
                            checked: enabled,
                            oninput: (e) => enabled.val = (e.target as HTMLInputElement).checked
                        }),
                        span({ class: 'form-check-label ms-2' }, '启用自动扫描')
                    )
                )
            ),
            div({ class: 'small text-secondary' },
                '最高可用会按 B 站返回的最高格式选择，顺序包含 8K、杜比视界、HDR、4K、1080P60 等；编码按优先项尝试，失败后自动回退。'
            ),
            div({ class: 'hstack gap-2 flex-wrap' },
                button({
                    class: 'btn btn-primary',
                    disabled: this.saving,
                    onclick: async () => {
                        this.saving.val = true
                        try {
                            this.settings = {
                                enabled: enabled.val,
                                favMediaId: Number(favMediaId.val),
                                intervalMinutes: Number(interval.val) || 10,
                                archiveFolder: archiveFolder.val.trim(),
                                downloadAllPages: allPages.val,
                                downloadType: downloadType.val,
                                format: format.val || 'highest',
                                preferredCodec: preferredCodec.val,
                                preferHiResAudio: preferHiResAudio.val,
                            }
                            await saveSettings(this.settings)
                        } catch (error) {
                            alert((error as Error).message)
                        } finally {
                            this.saving.val = false
                        }
                    }
                }, () => this.saving.val ? '保存中...' : '保存设置'),
                button({
                    class: 'btn btn-outline-primary',
                    disabled: this.scanning,
                    onclick: async () => {
                        this.scanning.val = true
                        this.scanMessage.val = '扫描已开始，新发现的视频会立即出现在归档列表顶部。'
                        try {
                            await scanFavorite()
                            while (true) {
                                this.items.val = await listArchive(this.query.val)
                                const state = await getScanStatus()
                                if (!state.running) {
                                    if (state.error) throw new Error(state.error)
                                    const result = state.result
                                    if (result) this.scanMessage.val = `发现 ${result.found} 个，新增 ${result.created} 个，跳过 ${result.skipped} 个，失败 ${result.failed} 个`
                                    break
                                }
                                await new Promise(resolve => setTimeout(resolve, 900))
                            }
                        } catch (error) {
                            alert((error as Error).message)
                        } finally {
                            this.scanning.val = false
                        }
                    }
                }, () => this.scanning.val ? '扫描中...' : '立即扫描'),
                div({ class: 'text-secondary small' }, this.scanMessage),
            )
        )
    }

    PlayerPanel() {
        return () => {
            const item = this.activeItem.val
            if (!item || item.status != 'done') return ''
            const src = `/api/downloadVideo?path=${encodeURIComponent(item.filePath)}`
            const previewSrc = `/api/archive/preview?path=${encodeURIComponent(item.filePath)}`
            const coverSrc = item.coverPath ? `/api/downloadVideo?path=${encodeURIComponent(item.coverPath)}` : ''
            const info = this.activeInfo.val
            const videoInfo = info?.video
            const stat = videoInfo?.stat
            const containerStyle = 'position: relative; width: 100%; height: max(360px, calc(100vh - 96px)); overflow: hidden;'
            const videoStyle = () => `display: block; width: 100%; height: 100%; object-fit: ${this.playerFitMode.val}; position: relative; z-index: 1;`
            return div({ class: 'vstack gap-2' },
                div({ class: 'h5 mb-0' }, item.part || item.title),
                div({ id: 'archive-player-container', class: 'bg-black text-center', style: containerStyle },
                    video({
                        id: 'archive-player-video',
                        style: videoStyle,
                        controls: true,
                        src,
                        onloadedmetadata: () => this.updatePlayerHeight(),
                        oncanplay: () => this.playerMessage.val = '',
                        onerror: (event) => {
                            const media = event.currentTarget as HTMLVideoElement
                            if (media.dataset.preview == '1') {
                                this.playerMessage.val = '浏览器仍无法播放此视频，请用“打开归档位置”调用本地播放器。'
                                return
                            }
                            media.dataset.preview = '1'
                            this.playerMessage.val = '浏览器不支持原始编码，正在生成 H.264/AAC 预览版...'
                            media.src = previewSrc
                            media.load()
                        }
                    }),
                    div({ id: 'archive-danmaku-layer', style: 'position: absolute; inset: 0; z-index: 2; overflow: hidden; pointer-events: none;' })
                ),
                div({ class: 'small text-secondary' }, this.playerMessage),
                div({ class: 'hstack gap-3 flex-wrap small' },
                    button({
                        class: 'btn btn-sm btn-outline-secondary',
                        onclick: () => {
                            this.danmakuVisible.val = !this.danmakuVisible.val
                            if (this.danmakuVisible.val) this.danmakuInstance?.show()
                            else this.danmakuInstance?.hide()
                        }
                    }, () => this.danmakuVisible.val ? '关闭弹幕' : '开启弹幕'),
                    label({ class: 'hstack gap-2' },
                        span({}, '显示模式'),
                        select({
                            class: 'form-select form-select-sm',
                            style: 'width: auto;',
                            value: this.playerFitMode,
                            oninput: (e) => {
                                this.playerFitMode.val = (e.target as HTMLSelectElement).value as typeof this.playerFitMode.val
                                this.danmakuInstance?.resize()
                            }
                        },
                            option({ value: 'contain' }, '适应'),
                            option({ value: 'cover' }, '填充'),
                            option({ value: 'fill' }, '拉伸')
                        )
                    ),
                    a({ href: `https://www.bilibili.com/video/${item.bvid}`, target: '_blank' }, item.bvid),
                    span({}, `CID ${item.cid}`),
                    a({ href: `/api/downloadVideo?path=${encodeURIComponent(item.infoPath)}`, target: '_blank' }, 'info.json'),
                    a({ href: `/api/downloadVideo?path=${encodeURIComponent(item.danmakuPath)}`, target: '_blank' }, '弹幕XML'),
                ),
                () => info ? div({ class: 'vstack gap-2 border-top pt-3 mt-2' },
                    div({ class: 'd-flex gap-3 align-items-start flex-wrap' },
                        coverSrc ? img({ src: coverSrc, style: 'width: 220px; max-width: 42vw; aspect-ratio: 16 / 9; object-fit: cover; border-radius: 4px;' }) : '',
                        div({ class: 'vstack gap-2 flex-fill' },
                            div({ class: 'fw-semibold' }, videoInfo?.title || item.title),
                            div({ class: 'small text-secondary' },
                                `${videoInfo?.owner?.name || item.owner} / 发布 ${videoInfo?.pubdate ? new Date(videoInfo.pubdate * 1000).toLocaleString() : '-'} / 归档 ${new Date(info.archived_at).toLocaleString()}`
                            ),
                            div({ class: 'small', style: 'white-space: pre-wrap;' }, videoInfo?.desc || '')
                        )
                    ),
                    stat ? div({ class: 'hstack gap-3 flex-wrap small text-secondary' },
                        span({}, `播放 ${stat.view ?? '-'}`),
                        span({}, `弹幕 ${stat.danmaku ?? '-'}`),
                        span({}, `评论 ${stat.reply ?? '-'}`),
                        span({}, `收藏 ${stat.favorite ?? '-'}`),
                        span({}, `投币 ${stat.coin ?? '-'}`),
                        span({}, `点赞 ${stat.like ?? '-'}`),
                    ) : '',
                ) : ''
            )
        }
    }

    ArchiveList() {
        const owners = van.derive(() => {
            return ['all'].concat([...new Set(this.items.val.map(item => item.owner).filter(Boolean))].sort())
        })
        const filteredItems = van.derive(() => {
            return this.items.val.filter(item => {
                if (this.statusFilter.val != 'all' && item.status != this.statusFilter.val) return false
                if (this.ownerFilter.val != 'all' && item.owner != this.ownerFilter.val) return false
                return true
            })
        })
        const selectedItems = van.derive(() => this.items.val.filter(item => this.selectedIds.val.has(item.id)))
        const selectedCount = van.derive(() => selectedItems.val.length)
        const errorSelectedCount = van.derive(() => selectedItems.val.filter(item => item.status == 'error' || item.status == 'unavailable').length)
        const allVisibleSelected = van.derive(() => filteredItems.val.length > 0 && filteredItems.val.every(item => this.selectedIds.val.has(item.id)))
        const toggleSelected = (item: ArchiveItem) => {
            const next = new Set(this.selectedIds.val)
            if (next.has(item.id)) next.delete(item.id)
            else next.add(item.id)
            this.selectedIds.val = next
        }
        const setVisibleSelected = (selected: boolean) => {
            const next = new Set(this.selectedIds.val)
            filteredItems.val.forEach(item => selected ? next.add(item.id) : next.delete(item.id))
            this.selectedIds.val = next
        }

        return div({ class: 'vstack gap-3' },
            div({ class: 'row g-2 align-items-center' },
                div({ class: 'col-12 col-lg' },
                input({
                    class: 'form-control',
                    placeholder: '搜索标题、UP主、BV',
                    value: this.query,
                    oninput: (e) => this.query.val = (e.target as HTMLInputElement).value,
                    onkeydown: async (e) => {
                        if ((e as KeyboardEvent).key == 'Enter') this.items.val = await listArchive(this.query.val)
                    }
                })),
                div({ class: 'col-6 col-md-3 col-lg-2' },
                    select({
                        class: 'form-select',
                        value: this.statusFilter,
                        oninput: (e) => this.statusFilter.val = (e.target as HTMLSelectElement).value as typeof this.statusFilter.val
                    },
                        option({ value: 'all' }, '全部状态'),
                        option({ value: 'done' }, '已完成'),
                        option({ value: 'error' }, '失败'),
                        option({ value: 'unavailable' }, '下架／不可用'),
                        option({ value: 'resolving' }, '读取信息中'),
                        option({ value: 'waiting' }, '等待'),
                        option({ value: 'running' }, '下载中')
                    )
                ),
                div({ class: 'col-6 col-md-3 col-lg-2' },
                    () => select({
                        class: 'form-select',
                        value: this.ownerFilter,
                        oninput: (e) => this.ownerFilter.val = (e.target as HTMLSelectElement).value
                    },
                        owners.val.map(owner => option({ value: owner }, owner == 'all' ? '全部UP主' : owner))
                    )
                ),
                div({ class: 'col-auto' },
                button({
                    class: 'btn btn-outline-secondary text-nowrap',
                    onclick: async () => this.items.val = await listArchive(this.query.val)
                }, '搜索')
                )
            ),
            div({ class: 'hstack gap-2 flex-wrap' },
                button({
                    class: 'btn btn-outline-secondary btn-sm',
                    disabled: () => filteredItems.val.length == 0,
                    onclick: () => setVisibleSelected(!allVisibleSelected.val)
                }, () => allVisibleSelected.val ? '取消选择当前列表' : `选择当前列表 (${filteredItems.val.length})`),
                button({
                    class: 'btn btn-outline-secondary btn-sm',
                    disabled: () => selectedCount.val != 1,
                    onclick: async () => {
                        const item = selectedItems.val[0]
                        if (!item) return
                        try { await revealArchiveItem(item.id) } catch (error) { alert((error as Error).message) }
                    }
                }, '打开归档位置'),
                button({
                    class: 'btn btn-outline-primary btn-sm',
                    disabled: () => errorSelectedCount.val == 0 || this.operating.val,
                    onclick: async () => {
                        const ids = selectedItems.val.filter(item => item.status == 'error' || item.status == 'unavailable').map(item => item.id)
                        if (ids.length == 0) return
                        this.operating.val = true
                        try {
                            const result = await retryArchiveItems(ids)
                            this.scanMessage.val = `重试 ${result.retried} 个，跳过 ${result.skipped} 个，失败 ${result.failed} 个`
                            await this.refresh()
                        } catch (error) {
                            alert((error as Error).message)
                        } finally {
                            this.operating.val = false
                        }
                    }
                }, () => `重试失败项 (${errorSelectedCount.val})`),
                button({
                    class: 'btn btn-outline-secondary btn-sm',
                    disabled: () => selectedCount.val == 0 || this.operating.val,
                    onclick: async () => {
                        this.operating.val = true
                        try {
                            const result = await deleteArchivePreviews(selectedItems.val.map(item => item.id))
                            this.scanMessage.val = `删除预览 ${result.deleted} 个，跳过 ${result.skipped} 个，失败 ${result.failed} 个`
                        } catch (error) {
                            alert((error as Error).message)
                        } finally {
                            this.operating.val = false
                        }
                    }
                }, () => `删除所选预览 (${selectedCount.val})`),
                button({
                    class: 'btn btn-outline-secondary btn-sm',
                    disabled: () => this.operating.val,
                    onclick: async () => {
                        if (!confirm('确定清空全部浏览器预览缓存吗？原始归档视频不会删除。')) return
                        this.operating.val = true
                        try {
                            const result = await deleteArchivePreviews([], true)
                            this.scanMessage.val = `清空预览 ${result.deleted} 个，失败 ${result.failed} 个`
                        } catch (error) {
                            alert((error as Error).message)
                        } finally {
                            this.operating.val = false
                        }
                    }
                }, '清空预览缓存'),
                button({
                    class: 'btn btn-outline-danger btn-sm',
                    disabled: () => selectedCount.val == 0 || this.operating.val,
                    onclick: async () => {
                        if (!confirm(`确定删除 ${selectedCount.val} 个归档吗？这会删除视频文件、info.json、封面、弹幕和数据库记录。`)) return
                        this.operating.val = true
                        try {
                            const result = await deleteArchiveItems(selectedItems.val.map(item => item.id))
                            this.scanMessage.val = `删除 ${result.deleted} 个，跳过 ${result.skipped} 个，失败 ${result.failed} 个`
                            this.activeItem.val = null
                            this.activeInfo.val = null
                            await this.refresh()
                        } catch (error) {
                            alert((error as Error).message)
                        } finally {
                            this.operating.val = false
                        }
                    }
                }, () => `删除所选 (${selectedCount.val})`),
                div({ class: 'small text-secondary' }, () => `当前显示 ${filteredItems.val.length} 个，已选 ${selectedCount.val} 个`)
            ),
            () => div({ class: 'list-group' },
                filteredItems.val.map(item => div({
                    class: () => `list-group-item list-group-item-action ${this.activeItem.val?.id == item.id ? 'active' : ''}`,
                    role: 'button',
                    onclick: async () => {
                        this.activeItem.val = item
                        this.activeInfo.val = null
                        this.playerMessage.val = ''
                        this.bindPlayerResize()
                        this.updatePlayerHeight()
                        this.destroyDanmaku()
                        try {
                            this.activeInfo.val = await getInfo(item.infoPath)
                        } catch { }
                        setTimeout(() => this.loadDanmaku(item), 80)
                    },
                },
                    div({ class: 'd-flex gap-3 align-items-center' },
                        input({
                            class: 'form-check-input flex-shrink-0',
                            type: 'checkbox',
                            checked: () => this.selectedIds.val.has(item.id),
                            onclick: (event) => {
                                event.stopPropagation()
                                toggleSelected(item)
                            },
                        }),
                        item.coverPath ? img({
                            src: `/api/downloadVideo?path=${encodeURIComponent(item.coverPath)}`,
                            loading: 'lazy',
                            style: 'width: 96px; height: 54px; object-fit: cover; border-radius: 4px; flex: 0 0 auto;'
                        }) : '',
                        div({ class: 'min-w-0 flex-fill' },
                            div({ class: 'd-flex justify-content-between gap-3' },
                                div({ class: 'text-truncate' },
                                    span({ class: 'me-2 badge bg-secondary' }, item.status),
                                    span({}, item.part || item.title)
                                ),
                                div({ class: 'text-nowrap small' }, item.owner)
                            ),
                            div({ class: 'small opacity-75 text-truncate mt-1' },
                                `${item.bvid} / CID ${item.cid} / P${item.page} / ${item.updatedAt}`
                            ),
                            () => item.message ? div({ class: 'small text-danger mt-1' }, item.message) : ''
                        ),
                    )
                ))
            )
        )
    }

    startArchivePolling() {
        if (this.archivePollTimer != null) return
        this.archivePollTimer = window.setInterval(async () => {
            try {
                this.items.val = await listArchive(this.query.val)
            } catch { }
        }, 3000)
    }

    destroyDanmaku() {
        if (this.danmakuResizeHandler) {
            window.removeEventListener('resize', this.danmakuResizeHandler)
            this.danmakuResizeHandler = null
        }
        this.danmakuInstance?.destroy()
        this.danmakuInstance = null
    }

    updatePlayerHeight() {
        this.danmakuInstance?.resize()
    }

    bindPlayerResize() {
        if (this.playerResizeHandler) return
        this.playerResizeHandler = () => this.updatePlayerHeight()
        window.addEventListener('resize', this.playerResizeHandler)
    }

    async loadDanmaku(item: ArchiveItem) {
        if (item.status != 'done') return
        const container = document.getElementById('archive-danmaku-layer')
        const media = document.getElementById('archive-player-video') as HTMLVideoElement | null
        if (!container || !media || !item.danmakuPath) return
        try {
            this.bindPlayerResize()
            this.updatePlayerHeight()
            const res = await fetch(`/api/downloadVideo?path=${encodeURIComponent(item.danmakuPath)}`)
            if (!res.ok) return
            const comments = parseBiliDanmakuXML(await res.text())
            if (media.readyState < 1) {
                await new Promise<void>(resolve => {
                    media.addEventListener('loadedmetadata', () => resolve(), { once: true })
                    setTimeout(resolve, 1200)
                })
            }
            this.destroyDanmaku()
            this.danmakuInstance = new Danmaku({
                container,
                media,
                comments,
                engine: 'canvas',
                speed: 144,
            })
            this.danmakuResizeHandler = () => this.danmakuInstance?.resize()
            window.addEventListener('resize', this.danmakuResizeHandler)
            requestAnimationFrame(() => this.danmakuInstance?.resize())
            if (!this.danmakuVisible.val) this.danmakuInstance.hide()
        } catch { }
    }
}

export default () => new ArchiveRoute().element

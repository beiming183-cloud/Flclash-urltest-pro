package com.follow.clash.service.modules

internal data class StabilityNetworkSnapshot(val networkKey: Long, val online: Boolean)

internal class StabilityNetworkObserver {
    private var current: StabilityNetworkSnapshot? = null

    fun update(
        networkKey: Long?,
        internet: Boolean,
        notVpn: Boolean,
        notSuspended: Boolean,
    ): StabilityNetworkSnapshot? {
        val next = StabilityNetworkSnapshot(
            networkKey = networkKey ?: 0L,
            online = networkKey != null && internet && notVpn && notSuspended,
        )
        if (next == current) return null
        current = next
        return next
    }
}

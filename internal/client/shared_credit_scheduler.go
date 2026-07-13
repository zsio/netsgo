package client

const sharedCreditMaxBlock = 256 * 1024

// fairCreditAllocation divides one newly available token block between the
// two directions. It is work-conserving: unused capacity is immediately lent
// to the busy direction. When both directions are backlogged, neither grant
// can consume the other's share or exceed the bounded scheduling block.
func fairCreditAllocation(available, ingressDemand, egressDemand uint64) (ingressGrant, egressGrant uint64) {
	if available == 0 {
		return 0, 0
	}
	capBlock := func(value uint64) uint64 {
		if value > sharedCreditMaxBlock {
			return sharedCreditMaxBlock
		}
		return value
	}
	ingressDemand, egressDemand = capBlock(ingressDemand), capBlock(egressDemand)
	if ingressDemand == 0 {
		return 0, minCredit(available, egressDemand)
	}
	if egressDemand == 0 {
		return minCredit(available, ingressDemand), 0
	}
	ingressShare := (available + 1) / 2
	egressShare := available / 2
	ingressGrant = minCredit(ingressShare, ingressDemand)
	egressGrant = minCredit(egressShare, egressDemand)
	remaining := available - ingressGrant - egressGrant
	if remaining > 0 {
		extraIngress := minCredit(remaining, ingressDemand-ingressGrant)
		ingressGrant += extraIngress
		remaining -= extraIngress
		egressGrant += minCredit(remaining, egressDemand-egressGrant)
	}
	return ingressGrant, egressGrant
}

func minCredit(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

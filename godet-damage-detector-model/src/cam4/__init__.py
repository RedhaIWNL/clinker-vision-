"""CAM-4 (day) pipeline: chain odometer -> unrolled chain -> overlap-fault detector ->
godet identity on the chain map -> per-godet history.

Streaming port of the calibration repo's cam4 package (unroll, detect, identity). The
service never imports cam4: every constant lives in model/cam4/calibration.json.
"""

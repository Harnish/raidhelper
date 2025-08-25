===raidhelper 
raidhelper is just a tool I put together to adjust the speed of raid check or rebuild.


It sets the appropriate kernel parameters to speed up or slow down the raid rebuild/check.  It can start or stop a check.  It can also reboot your machine once the check is complete.  

```
############################
# Currently Checking Raid  #
# Time left 1973.2min       #
# Speed Normal             #
############################
Available commands:
check       - returns >0 if the raid is checking
normal      - set speed normal
high        - set speed high
low         - set speed low
reboot      - Reboot the machine once the raid check is done
forcereboot - Stop raid check and reboot
stop        - stop raid check
start       - start raid check
```

#!/bin/sh


cd "$SM/sm_opt*" && echo "File sm_opt exists already. Abort." && exit

for RMS in 3
do

echo "$RMS replacement runs"

for Opt in "no"
do

for CP in "thrifty" #"norecontact" "thrifty"
do

for ALG in "sm"
do


cd $SM
echo Alg $ALG with optimization $Opt conf provider $CP


mkdir "$ALG-opt$Opt-cp$CP-repl$RMS$*"
for i in {1..40} 
do
	echo make run $i
	./scripts/leader-run.sh "$Opt" $ALG $CP -repl "$RMS" " " 0
	mv $SM/exlogs $SM/"$ALG-opt$Opt-cp$CP-repl$RMS$*"/"run$i"
	echo sleeping 3 seconds
	sleep 3
done
cd "$ALG-opt$Opt-cp$CP-repl$RMS$*"

echo checking
mkdir problem
for R in run*; do
	cd $R
	if ls ./*ERROR* > /dev/null 2>&1; then
		cd ..
		mv $R problem/
	fi
	cd $SM/"$ALG-opt$Opt-cp$CP-repl$RMS$*"
done
for R in run*; do
	$SM/scripts/checkall $R || mv $R problem/
done
rmdir problem || echo some runs had problems		
echo analysing
$SM/scripts/analyzeallsub analysis

: <<'END'
cd $SM
echo Alg $ALG with optimization $Opt regular conf prov $CP


mkdir "$ALG-regopt$Opt-cp$CP-repl$RMS$*"
for i in {1..40} 
do
	echo make run $i
	./scripts/sm-run.sh "$Opt" $ALG $CP -repl "$RMS" "-regular" 0
	mv $SM/exlogs $SM/"$ALG-regopt$Opt-cp$CP-repl$RMS$*"/"run$i"
	echo sleeping 3 seconds
	sleep 3
done
cd "$ALG-regopt$Opt-cp$CP-repl$RMS$*"

echo checking
mkdir problem
for R in run*; do
	cd $R
	if ls ./*ERROR* > /dev/null 2>&1; then
		cd ..
		mv $R problem/
	fi
	cd $SM/"$ALG-regopt$Opt-cp$CP-repl$RMS$*"
done
for R in run*; do
	$SM/scripts/checkall $R || mv $R problem/
done
rmdir problem || echo some runs had problems		
echo analysing
$SM/scripts/analyzeallsub analysis
END

: <<'END'

END

done #for ALG
done #for CP
done #for Opt
done #for RMS
